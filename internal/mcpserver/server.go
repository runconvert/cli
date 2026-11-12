// Package mcpserver exposes RunConvert as MCP tools, for AI apps such as
// Claude and ChatGPT.
//
// Local mode (stdio, `runconvert mcp`) can read and write files on this
// computer. Remote mode (HTTP, `runconvert mcp --http`) can't see the
// caller's files: it converts files from URLs and returns download links.
package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/runconvert/cli/internal/api"
	"github.com/runconvert/cli/internal/catalog"
	"github.com/runconvert/cli/internal/engine"
)

type Mode int

const (
	Local Mode = iota
	Remote
)

func boolPtr(b bool) *bool { return &b }

// ── Tool inputs and outputs ───────────────────────────────────────────────

type ListFormatsIn struct {
	From string `json:"from,omitempty" jsonschema:"an input format (mp4, heic, docx) or a category (video, image, document…); empty lists the categories"`
	To   string `json:"to,omitempty" jsonschema:"optional output format; when given, returns the credit cost and the options for this pair"`
}

type FormatOut struct {
	Format   string `json:"format"`
	Category string `json:"category,omitempty"`
	Group    string `json:"group,omitempty"` // Popular, Image, Document…
	Credits  int    `json:"credits,omitempty"`
}

type OptionOut struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	Description     string `json:"description"`
	SupportedValues any    `json:"supported_values,omitempty"`
}

type ListFormatsOut struct {
	Categories []CategoryOut `json:"categories,omitempty"`
	Formats    []FormatOut   `json:"formats,omitempty"` // a category's input formats
	From       string        `json:"from,omitempty"`
	To         string        `json:"to,omitempty"`
	Outputs    []FormatOut   `json:"outputs,omitempty"`
	Engine     string        `json:"engine,omitempty"`
	Credits    int           `json:"credits,omitempty"`
	Options    []OptionOut   `json:"options,omitempty"`
}

type CategoryOut struct {
	Name    string `json:"name"`
	Formats int    `json:"formats"`
}

type AccountOut struct {
	Email   string `json:"email,omitempty"`
	Team    string `json:"team,omitempty"`
	Credits string `json:"credits,omitempty"`
	Key     string `json:"key,omitempty"`
}

type ConvertIn struct {
	Sources   []string       `json:"sources" jsonschema:"what to convert: file paths, folders or http(s) URLs (remote server: URLs only)"`
	To        string         `json:"to" jsonschema:"output format as a file extension, for example png, mp3, pdf"`
	From      string         `json:"from,omitempty" jsonschema:"input format, only needed when it can't be read from the file name"`
	OutDir    string         `json:"out_dir,omitempty" jsonschema:"folder to save results in; default is next to each input file (local only)"`
	Recursive bool           `json:"recursive,omitempty" jsonschema:"include subfolders when a source is a folder (local only)"`
	Overwrite bool           `json:"overwrite,omitempty" jsonschema:"replace output files that already exist (local only); default false skips them"`
	Options   map[string]any `json:"options,omitempty" jsonschema:"engine options by exact name, from list_formats with from and to, for example {\"image_quality\": 90}"`
}

type ConvertOut struct {
	Converted int             `json:"converted"`
	Failed    int             `json:"failed"`
	Pending   int             `json:"pending,omitempty"`
	Note      string          `json:"note,omitempty"`
	Results   []engine.Result `json:"results"`
}

type GetJobIn struct {
	JobID string `json:"job_id" jsonschema:"the job id returned by convert"`
}

type GetJobOut struct {
	ID      string     `json:"id"`
	Status  string     `json:"status"`
	Percent int        `json:"percent"`
	Error   string     `json:"error,omitempty"`
	Files   []api.File `json:"files,omitempty"`
}

// ── Server ────────────────────────────────────────────────────────────────

const maxSources = 20

// RemoteWait is how long a remote convert call waits before returning job ids
// for conversions still running (Cloudflare's proxy limit is 100 seconds).
var RemoteWait = 60 * time.Second

// New builds an MCP server whose tools act with the given API client.
func New(c *api.Client, mode Mode, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "runconvert",
		Title:   "RunConvert",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "RunConvert converts files between 3,000+ formats (video, audio, image, document, ebook, presentation, spreadsheet, vector, CAD, archive, font). " +
			"Use list_formats to check a conversion exists and to find option names (pass them in convert's options), then convert. " +
			"Each conversion costs credits from the user's account; account shows how many are left. " +
			"Before converting many files, say how many and roughly how many credits.",
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_formats",
		Description: "What RunConvert converts: with no arguments the 11 categories; with a category (video, image…) its formats; with a format every output, popular first; with from and to the credit cost and the options for that conversion.",
		Annotations: &mcp.ToolAnnotations{Title: "List formats", ReadOnlyHint: true, OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListFormatsIn) (*mcp.CallToolResult, ListFormatsOut, error) {
		from := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(in.From)), ".")
		to := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(in.To)), ".")
		out := ListFormatsOut{From: from, To: to}
		if from == "" {
			cats, all, err := c.Catalog(ctx, "")
			if err != nil {
				return nil, out, err
			}
			count := map[string]int{}
			for _, f := range all {
				count[f.Category]++
			}
			for _, cat := range cats {
				out.Categories = append(out.Categories, CategoryOut{Name: cat, Formats: count[cat]})
			}
			return nil, out, nil
		}
		if cat := catalog.Categories[from]; cat != "" && to == "" {
			_, list, err := c.Catalog(ctx, cat)
			if err != nil {
				return nil, out, err
			}
			for _, f := range list {
				out.Formats = append(out.Formats, FormatOut{Format: f.Format, Category: f.Category})
			}
			return nil, out, nil
		}
		if to == "" {
			f, err := c.Outputs(ctx, from)
			if err != nil {
				return nil, out, err
			}
			for _, g := range catalog.Grouped(f) {
				for _, o := range g.Formats {
					out.Outputs = append(out.Outputs, FormatOut{Format: o.Format, Category: o.Category, Group: g.Name, Credits: o.Credits})
				}
			}
			return nil, out, nil
		}
		engines, err := c.Engines(ctx, from, to)
		if err != nil {
			return nil, out, err
		}
		if len(engines) > 0 {
			e := engines[0]
			out.Engine, out.Credits = e.Name, e.Credits
			for _, o := range e.Options {
				out.Options = append(out.Options, OptionOut{Name: o.Name, Type: o.Type, Description: o.Description, SupportedValues: o.SupportedValues})
			}
		}
		return nil, out, nil
	})

	desc := "Convert files to another format. Sources can be file paths, folders or URLs; results are saved next to the inputs (or in out_dir) and the saved paths are returned."
	if mode == Remote {
		desc = "Convert files from public http(s) URLs to another format and return download links (valid for a limited time). Conversions still running after about a minute come back with status processing and a job_id: call get_job with it later for the link."
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "convert",
		Description: desc,
		Annotations: &mcp.ToolAnnotations{Title: "Convert files", DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ConvertIn) (*mcp.CallToolResult, ConvertOut, error) {
		var out ConvertOut
		to := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(in.To)), ".")
		if to == "" {
			return nil, out, errors.New("to is required, for example \"png\"")
		}
		if len(in.Sources) == 0 {
			return nil, out, errors.New("sources is empty")
		}
		if mode == Remote {
			for _, src := range in.Sources {
				if !engine.IsURL(src) {
					return nil, out, fmt.Errorf("%q is not a URL; the remote RunConvert server can only fetch files from http(s) URLs. Use the local server (runconvert mcp) for files on this computer", src)
				}
			}
		}
		inputs, err := engine.Expand(in.Sources, in.Recursive, in.From)
		if err != nil {
			return nil, out, err
		}
		if len(inputs) > maxSources {
			return nil, out, fmt.Errorf("%d files found; convert at most %d per call", len(inputs), maxSources)
		}
		opts := engine.Options{To: to, OutDir: in.OutDir, Overwrite: in.Overwrite, EngineOptions: in.Options, Parallel: 3}
		if mode == Remote {
			opts.OutDir = ""
			// Cloudflare ends requests after 100 s: answer in time and let the
			// app pick up slow conversions with get_job.
			opts.Deadline = time.Now().Add(RemoteWait)
		}
		for i, input := range inputs {
			r := engine.One(ctx, c, input, opts, mode == Local, i, func(engine.Event) {})
			switch {
			case r.Error != "":
				out.Failed++
			case r.Status == "processing":
				out.Pending++
			default:
				out.Converted++
			}
			out.Results = append(out.Results, r)
		}
		if out.Pending > 0 {
			out.Note = "Some conversions are still running. Call get_job with each job_id in a little while to get the download link."
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_job",
		Description: "Check a conversion job's status and get its download links.",
		Annotations: &mcp.ToolAnnotations{Title: "Get job", ReadOnlyHint: true, OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetJobIn) (*mcp.CallToolResult, GetJobOut, error) {
		job, err := c.GetJob(ctx, strings.TrimSpace(in.JobID))
		if err != nil {
			return nil, GetJobOut{}, err
		}
		out := GetJobOut{ID: job.ID, Status: job.Status}
		if t := job.Task("convert-1"); t != nil {
			out.Percent = t.Percent
		}
		if job.Status == "error" {
			out.Error = job.Failure()
		}
		for _, t := range job.Tasks {
			if t.Operation == "export/url" {
				out.Files = append(out.Files, t.Files()...)
			}
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "account",
		Description: "The signed-in RunConvert account: email, team and credits left.",
		Annotations: &mcp.ToolAnnotations{Title: "Account", ReadOnlyHint: true, OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, AccountOut, error) {
		me, err := c.Me(ctx)
		if e, ok := api.IsAPIError(err); ok && e.Status == 404 {
			return nil, AccountOut{}, errors.New("this RunConvert server doesn't share account details yet; conversions still work")
		}
		if err != nil {
			return nil, AccountOut{}, err
		}
		out := AccountOut{Email: me.User.Email, Team: me.Team.Name, Credits: me.CreditsText()}
		if me.APIKey != nil {
			out.Key = me.APIKey.Name
		}
		return nil, out, nil
	})

	return s
}

// ── Remote (HTTP) ─────────────────────────────────────────────────────────

// KeyFromRequest reads the caller's API key: an Authorization: Bearer header,
// or the last path segment of /mcp/<key> for clients that can only be given a
// URL (until OAuth is added).
func KeyFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, "/mcp/"); ok {
		if k := filepath.Base(rest); strings.HasPrefix(k, "rc_live_") {
			return k
		}
	}
	return ""
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return strings.TrimRight(v, "/")
	}
	return def
}

// publicURL is where this server is reached (RUNCONVERT_MCP_PUBLIC_URL).
func publicURL() string { return envOr("RUNCONVERT_MCP_PUBLIC_URL", "https://mcp.runconvert.com") }

// authServerURL is the OAuth authorization server (RUNCONVERT_AUTH_SERVER).
func authServerURL() string { return envOr("RUNCONVERT_AUTH_SERVER", "https://api.runconvert.com") }

// trustForwarded is set when the server runs behind RunConvert's own nginx
// (RUNCONVERT_TRUST_PROXY=1), which sets X-Real-IP itself.
var trustForwarded = os.Getenv("RUNCONVERT_TRUST_PROXY") == "1"

// Handler serves the remote MCP server at /mcp and /mcp/<key>. Each request
// gets a server bound to its caller's key; the server keeps no state.
func Handler(version, userAgent string, logger *slog.Logger) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		c := api.New(KeyFromRequest(r), userAgent)
		// nginx sets X-Real-IP to the visitor's address (Cloudflare real IP).
		if trustForwarded {
			c.ForwardIP = r.Header.Get("X-Real-IP")
		}
		return New(c, Remote, version)
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: logger})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	// OAuth protected-resource metadata (RFC 9728): tells AI apps to sign in
	// at api.runconvert.com; the access token they get is an API key.
	public, authServer := publicURL(), authServerURL()
	metadataURL := public + "/.well-known/oauth-protected-resource"
	meta := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		json.NewEncoder(w).Encode(map[string]any{
			"resource":                 public + "/mcp",
			"authorization_servers":    []string{authServer},
			"scopes_supported":         []string{"convert"},
			"bearer_methods_supported": []string{"header"},
			"resource_name":            "RunConvert",
			"resource_documentation":   "https://www.runconvert.com/docs",
		})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", meta)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", meta)

	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := KeyFromRequest(r)
		if !strings.HasPrefix(key, "rc_live_") {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q, scope="convert"`, metadataURL))
			http.Error(w, "Sign in to RunConvert (OAuth), or send Authorization: Bearer rc_live_... with an API key.", http.StatusUnauthorized)
			return
		}
		start := time.Now()
		h.ServeHTTP(w, r)
		// Never log the path or headers: they may carry the key.
		if logger != nil {
			sum := sha256.Sum256([]byte(key))
			logger.Info("mcp request", "method", r.Method, "key", fmt.Sprintf("%x", sum[:4]), "ms", time.Since(start).Milliseconds())
		}
	})
	mux.Handle("/mcp", authed)
	mux.Handle("/mcp/", authed)
	return mux
}
