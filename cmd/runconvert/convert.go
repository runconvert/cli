package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/runconvert/runconvert-cli/internal/catalog"
	"github.com/runconvert/runconvert-cli/internal/engine"
)

func convertCmd() *cobra.Command {
	var (
		o         engine.Options
		from      string
		only      string
		recursive bool
		opts      []string
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "convert <files, folders, globs or URLs...> --to <format>",
		Short: "Convert files",
		Long: "Convert files, folders, glob patterns or URLs to another format.\n\n" +
			"By default each result is saved next to its input. --out saves into a folder;\n" +
			"-o takes a pattern with {dir}, {name}, {ext} (new format) and {from} (old one).",
		Example: "  runconvert convert photo.heic --to jpg\n" +
			"  runconvert convert ./images/*.webp --to png --out ./public/png\n" +
			"  runconvert convert \"./Holiday Photos\" --to jpg -r --keep-structure --out ./converted\n" +
			"  runconvert convert ~/Downloads --only video --to mp4\n" +
			"  runconvert convert talk.mp4 --to mp3 -o \"./audio/{name}.{ext}\" --opt audioBitrate=192k\n" +
			"  runconvert convert https://example.com/report.docx --to pdf\n" +
			"  runconvert convert *.png --to webp --json",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if o.To = strings.TrimPrefix(strings.ToLower(o.To), "."); o.To == "" {
				return errors.New("--to is required, e.g. --to png (see `runconvert formats <from>`)")
			}
			c, err := client(true)
			if err != nil {
				return err
			}
			inputs, err := engine.Expand(args, recursive, from)
			if err != nil {
				return err
			}
			cat := catalog.NewCache(c)
			if only != "" {
				name := catalog.Categories[strings.ToLower(only)]
				if name == "" {
					return fmt.Errorf("unknown category %q; see `runconvert formats`", only)
				}
				if inputs, _, err = engine.OnlyCategory(ctx, cat, inputs, name); err != nil {
					return err
				}
			}
			if len(inputs) == 0 {
				return errors.New("no files found")
			}
			if len(opts) > 0 {
				engines, err := cat.Engines(ctx, inputs[0].Format, o.To)
				if err != nil {
					return err
				}
				if o.EngineOptions, err = catalog.TypedOptions(engines, opts); err != nil {
					return err
				}
			}

			pr := newPrinter(len(inputs), asJSON)
			start := time.Now()
			results := engine.All(ctx, c, inputs, o, pr.event)
			pr.clear()

			failed := 0
			for _, r := range results {
				if r.Error != "" {
					failed++
				}
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				_ = enc.Encode(results)
			} else {
				fmt.Fprintf(os.Stderr, "\n%d converted, %d failed in %s\n", len(results)-failed, failed, time.Since(start).Round(100*time.Millisecond))
			}
			if failed > 0 {
				return fmt.Errorf("%d of %d conversions failed", failed, len(results))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVarP(&o.To, "to", "t", "", "output format, e.g. png, mp3, pdf (required)")
	f.StringVar(&from, "from", "", "input format, when it can't be read from the file name")
	f.StringVar(&o.OutDir, "out", "", "folder to save results in (default: next to each input)")
	f.StringVarP(&o.Pattern, "output", "o", "", "output path pattern: {dir} {name} {ext} {from}")
	f.BoolVarP(&recursive, "recursive", "r", false, "include subfolders of folder inputs")
	f.StringVar(&only, "only", "", "only one category: image, video, audio, document, ebook, presentation, spreadsheet, vector, cad, archive, font")
	f.BoolVar(&o.KeepStructure, "keep-structure", false, "recreate subfolders in the output")
	f.BoolVar(&o.Overwrite, "overwrite", false, "replace results that already exist (default: skip them)")
	f.StringVar(&o.Engine, "engine", "", "preferred engine, when a conversion has several")
	f.StringArrayVar(&opts, "opt", nil, "conversion setting name=value, repeatable (names: runconvert formats <from> <to>)")
	f.IntVarP(&o.Parallel, "parallel", "p", 3, "how many files to convert at once")
	f.BoolVar(&asJSON, "json", false, "print results as JSON (for scripts)")
	groupFlags(cmd, []flagGroup{
		{"Format", []string{"to", "from"}},
		{"Which files", []string{"recursive", "only"}},
		{"Where results go", []string{"out", "output", "keep-structure", "overwrite"}},
		{"Conversion settings", []string{"opt", "engine"}},
		{"Running", []string{"parallel", "json"}},
	})
	return cmd
}

// printer shows a line per finished file and, in a terminal, a live status line.
type printer struct {
	total, done int
	json, tty   bool
	state       map[int]string
}

func newPrinter(total int, asJSON bool) *printer {
	st, _ := os.Stderr.Stat()
	return &printer{total: total, json: asJSON, tty: st != nil && st.Mode()&os.ModeCharDevice != 0, state: map[int]string{}}
}

func (p *printer) event(e engine.Event) {
	if p.json {
		return
	}
	name := e.Input.Name()
	switch e.Stage {
	case "upload":
		p.state[e.Index] = fmt.Sprintf("%s uploading %d%%", name, e.Percent)
	case "convert":
		p.state[e.Index] = fmt.Sprintf("%s converting %d%%", name, e.Percent)
	case "download":
		p.state[e.Index] = name + " saving"
	case "done", "skip", "error":
		delete(p.state, e.Index)
		p.done++
		p.clear()
		switch e.Stage {
		case "done":
			fmt.Fprintf(os.Stderr, "✓ %s → %s\n", name, e.Dest)
		case "skip":
			fmt.Fprintf(os.Stderr, "• %s skipped: %s exists (use --overwrite)\n", name, e.Dest)
		default:
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", name, e.Err)
		}
	}
	p.status()
}

func (p *printer) clear() {
	if p.tty {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
}

func (p *printer) status() {
	if !p.tty || p.done == p.total {
		return
	}
	keys := make([]int, 0, len(p.state))
	for k := range p.state {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, p.state[k])
	}
	line := fmt.Sprintf("[%d/%d] %s", p.done, p.total, strings.Join(parts, " · "))
	if len(line) > 110 {
		line = line[:107] + "..."
	}
	p.clear()
	fmt.Fprint(os.Stderr, line)
}
