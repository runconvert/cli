// Package engine turns inputs (files, globs, folders, URLs) into RunConvert
// jobs and saves the results. The CLI and the MCP server both use it.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/runconvert/cli/internal/api"
	"github.com/runconvert/cli/internal/catalog"
)

// Input is one thing to convert: a local file or a URL.
type Input struct {
	Path   string // local file (absolute), empty for URLs
	URL    string // remote file
	Rel    string // path relative to the folder it was found in (for --keep-structure)
	Base   string // the folder it was found in, or the file's own folder
	Format string // input format (extension, lower case, no dot)
}

func (in Input) Name() string {
	if in.Path != "" {
		return filepath.Base(in.Path)
	}
	u, err := url.Parse(in.URL)
	if err == nil && u.Path != "" && u.Path != "/" {
		return filepath.Base(u.Path)
	}
	return in.URL
}

func IsURL(s string) bool {
	return strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")
}

func ext(name string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
}

// Expand resolves arguments into inputs: URLs, files, glob patterns and
// folders (only top level unless recursive). Hidden files are skipped in folders.
func Expand(args []string, recursive bool, from string) ([]Input, error) {
	var out []Input
	seen := map[string]bool{}
	add := func(in Input) {
		key := in.Path + in.URL
		if seen[key] {
			return
		}
		seen[key] = true
		if from != "" {
			in.Format = strings.ToLower(from)
		}
		out = append(out, in)
	}
	for _, arg := range args {
		if IsURL(arg) {
			in := Input{URL: arg}
			in.Format = ext(in.Name())
			in.Rel = in.Name()
			add(in)
			continue
		}
		matches := []string{arg}
		if strings.ContainsAny(arg, "*?[") {
			m, err := filepath.Glob(arg)
			if err != nil {
				return nil, fmt.Errorf("bad pattern %q: %w", arg, err)
			}
			if len(m) == 0 {
				return nil, fmt.Errorf("no files match %q", arg)
			}
			matches = m
		}
		for _, m := range matches {
			abs, err := filepath.Abs(m)
			if err != nil {
				return nil, err
			}
			st, err := os.Stat(abs)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", m, err)
			}
			if !st.IsDir() {
				add(Input{Path: abs, Base: filepath.Dir(abs), Rel: filepath.Base(abs), Format: ext(abs)})
				continue
			}
			err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				name := d.Name()
				if p != abs && strings.HasPrefix(name, ".") {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if d.IsDir() {
					if p != abs && !recursive {
						return filepath.SkipDir
					}
					return nil
				}
				if !d.Type().IsRegular() {
					return nil
				}
				rel, _ := filepath.Rel(abs, p)
				add(Input{Path: p, Base: abs, Rel: rel, Format: ext(p)})
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path+out[i].URL < out[j].Path+out[j].URL })
	return out, nil
}

// Options control where results go and how the conversion runs.
type Options struct {
	To            string
	OutDir        string // folder to save into; "" = next to each input (URLs: current folder)
	Pattern       string // output path pattern, e.g. "{dir}/{name}.{ext}"; overrides OutDir
	KeepStructure bool
	Overwrite     bool
	Engine        string
	EngineOptions map[string]any
	Parallel      int
	// Deadline, if set, is when to stop waiting for conversions: unfinished
	// ones come back with Status "processing" and their JobID. The remote MCP
	// server uses it to answer before Cloudflare's 100-second limit.
	Deadline time.Time
}

// Dest works out the output path for an input.
func Dest(in Input, o Options) (string, error) {
	name := strings.TrimSuffix(in.Name(), filepath.Ext(in.Name()))
	if name == "" {
		name = "converted"
	}
	to := strings.ToLower(o.To)
	dir := in.Base
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if o.KeepStructure {
		if sub := filepath.Dir(in.Rel); sub != "." {
			dir = filepath.Join(dir, sub)
		}
	}
	if o.Pattern != "" {
		r := strings.NewReplacer("{dir}", dir, "{name}", name, "{ext}", to, "{from}", in.Format)
		p := r.Replace(o.Pattern)
		return filepath.Abs(p)
	}
	if o.OutDir != "" {
		base, err := filepath.Abs(o.OutDir)
		if err != nil {
			return "", err
		}
		if o.KeepStructure {
			if sub := filepath.Dir(in.Rel); sub != "." {
				base = filepath.Join(base, sub)
			}
		}
		dir = base
	}
	return filepath.Join(dir, name+"."+to), nil
}

// Event reports progress for one input.
type Event struct {
	Index   int
	Input   Input
	Stage   string // upload | convert | download | done | error | skip | pending
	Percent int
	Dest    string
	JobID   string
	Err     error
}

// Result is the outcome for one input.
type Result struct {
	Input  Input  `json:"-"`
	Source string `json:"source"`
	Output string `json:"output,omitempty"`
	URL    string `json:"url,omitempty"`
	JobID  string `json:"job_id,omitempty"`
	// Status is "processing" when Deadline passed first; get the result later
	// with the job id.
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

func tasksFor(in Input, o Options) map[string]any {
	imp := map[string]any{"operation": "import/direct/upload"}
	if in.URL != "" {
		imp = map[string]any{"operation": "import/url", "source_url": in.URL}
	}
	conv := map[string]any{
		"operation":     "convert",
		"input":         "import-1",
		"input_format":  in.Format,
		"output_format": strings.ToLower(o.To),
	}
	if o.Engine != "" {
		conv["preferred_engine"] = o.Engine
	}
	for k, v := range o.EngineOptions {
		conv[k] = v
	}
	return map[string]any{
		"import-1":  imp,
		"convert-1": conv,
		"export-1":  map[string]any{"operation": "export/url", "input": "convert-1"},
	}
}

// One converts a single input. If download is false the result URL is
// returned without saving (used by the remote MCP server).
func One(ctx context.Context, c *api.Client, in Input, o Options, download bool, idx int, emit func(Event)) Result {
	res := Result{Input: in, Source: in.Path + in.URL}
	fail := func(err error) Result {
		res.Error = err.Error()
		emit(Event{Index: idx, Input: in, Stage: "error", Err: err, JobID: res.JobID})
		return res
	}
	if in.Format == "" {
		return fail(errors.New("can't tell the input format from the name; pass --from"))
	}
	dest := ""
	if download {
		d, err := Dest(in, o)
		if err != nil {
			return fail(err)
		}
		dest = d
		if !o.Overwrite {
			if _, err := os.Stat(dest); err == nil {
				res.Output = dest
				emit(Event{Index: idx, Input: in, Stage: "skip", Dest: dest})
				return res
			}
		}
		if in.Path != "" && sameFile(dest, in.Path) {
			return fail(errors.New("output would overwrite the input; use --out or -o"))
		}
	}

	job, err := c.CreateJob(ctx, "runconvert-cli", tasksFor(in, o))
	if err != nil {
		return fail(err)
	}
	res.JobID = job.ID

	if in.Path != "" {
		t := job.Task("import-1")
		if t == nil || t.UploadURL == "" {
			return fail(errors.New("the API did not return an upload URL"))
		}
		last := -1
		err := c.Upload(ctx, t.UploadURL, in.Path, func(done, total int64) {
			p := 100
			if total > 0 {
				p = int(done * 100 / total)
			}
			if p != last {
				last = p
				emit(Event{Index: idx, Input: in, Stage: "upload", Percent: p, JobID: job.ID})
			}
		})
		if err != nil {
			return fail(err)
		}
	}

	pending := func() Result {
		res.Status = "processing"
		emit(Event{Index: idx, Input: in, Stage: "pending", JobID: res.JobID})
		return res
	}
	waitCtx := ctx
	if !o.Deadline.IsZero() {
		if !time.Now().Before(o.Deadline) {
			return pending()
		}
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithDeadline(ctx, o.Deadline)
		defer cancel()
	}
	job, err = c.WaitJob(waitCtx, job.ID, func(j *api.Job) {
		if t := j.Task("convert-1"); t != nil {
			emit(Event{Index: idx, Input: in, Stage: "convert", Percent: t.Percent, JobID: j.ID})
		}
	})
	if err != nil {
		if ctx.Err() == nil && waitCtx.Err() != nil {
			return pending()
		}
		return fail(err)
	}
	if job.Status != "finished" {
		return fail(errors.New(job.Failure()))
	}
	files := job.Task("export-1").Files()
	if len(files) == 0 {
		return fail(errors.New("the job finished but returned no file"))
	}
	res.URL = files[0].URL
	if !download {
		emit(Event{Index: idx, Input: in, Stage: "done", JobID: job.ID})
		return res
	}
	emit(Event{Index: idx, Input: in, Stage: "download", JobID: job.ID, Dest: dest})
	if err := c.Download(ctx, files[0].URL, dest); err != nil {
		return fail(err)
	}
	res.Output = dest
	emit(Event{Index: idx, Input: in, Stage: "done", Dest: dest, JobID: job.ID})
	return res
}

func sameFile(a, b string) bool {
	x, err1 := filepath.Abs(a)
	y, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && x == y
}

// All converts every input, a few at a time.
func All(ctx context.Context, c *api.Client, inputs []Input, o Options, emit func(Event)) []Result {
	n := o.Parallel
	if n < 1 {
		n = 1
	}
	results := make([]Result, len(inputs))
	var mu sync.Mutex
	safe := func(e Event) { mu.Lock(); defer mu.Unlock(); emit(e) }
	sem := make(chan struct{}, n)
	var wg sync.WaitGroup
	for i, in := range inputs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = One(ctx, c, in, o, true, i, safe)
		}()
	}
	wg.Wait()
	return results
}

// OnlyCategory keeps the inputs whose format is in a category (image,
// video, audio…), and returns how many were left out.
func OnlyCategory(ctx context.Context, cat *catalog.Cache, inputs []Input, category string) ([]Input, int, error) {
	var kept []Input
	other := 0
	for _, in := range inputs {
		c, err := cat.Category(ctx, in.Format)
		if err != nil {
			return nil, 0, err
		}
		if c == category {
			kept = append(kept, in)
		} else {
			other++
		}
	}
	return kept, other, nil
}
