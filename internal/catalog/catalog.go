// Package catalog knows RunConvert's formats: the 11 categories, what each
// format converts to (grouped, most-used first) and each conversion's options.
// Everything comes from GET /v3/operations, so new formats appear on their own.
package catalog

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/runconvert/cli/internal/api"
)

// Categories maps the words people use to the API's category names.
var Categories = map[string]string{
	"image": "image", "images": "image", "photo": "image", "photos": "image", "picture": "image", "pictures": "image", "pics": "image", "screenshots": "image",
	"video": "video", "videos": "video", "clips": "video", "movies": "video",
	"audio": "audio", "music": "audio", "songs": "audio", "sounds": "audio", "recordings": "audio",
	"document": "document", "documents": "document", "docs": "document",
	"ebook": "ebook", "ebooks": "ebook", "books": "ebook",
	"presentation": "presentation", "presentations": "presentation", "slides": "presentation",
	"spreadsheet": "spreadsheet", "spreadsheets": "spreadsheet", "sheets": "spreadsheet",
	"vector": "vector", "vectors": "vector",
	"cad": "cad", "drawings": "cad",
	"archive": "archive", "archives": "archive",
	"font": "font", "fonts": "font",
}

// Popular lists the outputs people pick most, per input category.
var Popular = map[string][]string{
	"image":        {"jpg", "png", "webp", "pdf", "avif"},
	"video":        {"mp4", "mp3", "gif", "webm", "mov"},
	"audio":        {"mp3", "wav", "m4a", "flac", "ogg"},
	"document":     {"pdf", "docx", "txt", "html", "md"},
	"ebook":        {"pdf", "epub", "mobi", "azw3", "docx"},
	"presentation": {"pdf", "pptx", "png", "jpg", "odp"},
	"spreadsheet":  {"xlsx", "csv", "pdf", "ods", "html"},
	"vector":       {"svg", "pdf", "png", "eps", "jpg"},
	"cad":          {"pdf", "svg", "png", "dxf", "dwg"},
	"archive":      {"zip", "7z", "tar", "rar", "tar.gz"},
	"font":         {"ttf", "otf", "woff2", "woff"},
}

// Group is a row of output formats ("Popular", "Image", "Document"…).
type Group struct {
	Name    string
	Formats []api.Output
}

// Grouped returns every output of an input format once (the API lists a
// format once per engine), the popular ones first, the rest by category.
func Grouped(in *api.InputFormat) []Group {
	var outs []api.Output
	seen := map[string]bool{in.Format: true}
	for _, o := range in.Outputs {
		if !seen[o.Format] {
			seen[o.Format] = true
			outs = append(outs, o)
		}
	}
	var popular []api.Output
	for _, f := range Popular[in.Category] {
		if i := slices.IndexFunc(outs, func(o api.Output) bool { return o.Format == f }); i >= 0 {
			popular = append(popular, outs[i])
		}
	}
	groups := []Group{}
	if len(popular) > 0 {
		groups = append(groups, Group{Name: "Popular", Formats: popular})
	}
	byCat := map[string][]api.Output{}
	var cats []string
	for _, o := range outs {
		if slices.ContainsFunc(popular, func(p api.Output) bool { return p.Format == o.Format }) {
			continue
		}
		if _, ok := byCat[o.Category]; !ok {
			cats = append(cats, o.Category)
		}
		byCat[o.Category] = append(byCat[o.Category], o)
	}
	// The input's own category first, then the others alphabetically.
	sort.SliceStable(cats, func(i, j int) bool {
		if (cats[i] == in.Category) != (cats[j] == in.Category) {
			return cats[i] == in.Category
		}
		return cats[i] < cats[j]
	})
	for _, c := range cats {
		list := byCat[c]
		sort.Slice(list, func(i, j int) bool { return list[i].Format < list[j].Format })
		groups = append(groups, Group{Name: Title(c), Formats: list})
	}
	return groups
}

// Title capitalises a category name for display.
func Title(s string) string {
	if s == "" {
		return "Other"
	}
	if s == "cad" {
		return "CAD"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// TypedOptions turns name=value pairs into the API's option values, using the
// types it declares for this conversion. Unknown names are errors.
func TypedOptions(engines []api.Engine, pairs []string) (map[string]any, error) {
	out := map[string]any{}
	types := map[string]string{}
	for _, e := range engines {
		for _, o := range e.Options {
			types[o.Name] = o.Type
		}
	}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("option %q must be name=value", p)
		}
		t, known := types[k]
		if !known {
			return nil, fmt.Errorf("unknown option %q; see `runconvert formats <from> <to>`", k)
		}
		switch t {
		case "integer":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("option %s needs a whole number, got %q", k, v)
			}
			out[k] = n
		case "number":
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("option %s needs a number, got %q", k, v)
			}
			out[k] = n
		case "boolean":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, fmt.Errorf("option %s needs true or false, got %q", k, v)
			}
			out[k] = b
		default:
			out[k] = v
		}
	}
	return out, nil
}

// Cache remembers API answers for the life of the process.
type Cache struct {
	C       *api.Client
	outputs map[string]*api.InputFormat
	engines map[string][]api.Engine
}

func NewCache(c *api.Client) *Cache {
	return &Cache{C: c, outputs: map[string]*api.InputFormat{}, engines: map[string][]api.Engine{}}
}

func (c *Cache) Outputs(ctx context.Context, from string) (*api.InputFormat, error) {
	if f, ok := c.outputs[from]; ok {
		return f, nil
	}
	f, err := c.C.Outputs(ctx, from)
	if err != nil {
		return nil, err
	}
	c.outputs[from] = f
	return f, nil
}

func (c *Cache) Engines(ctx context.Context, from, to string) ([]api.Engine, error) {
	k := from + ">" + to
	if e, ok := c.engines[k]; ok {
		return e, nil
	}
	e, err := c.C.Engines(ctx, from, to)
	if err != nil {
		return nil, err
	}
	c.engines[k] = e
	return e, nil
}

// Category returns a format's category, or "" if the API doesn't know it.
func (c *Cache) Category(ctx context.Context, format string) (string, error) {
	f, err := c.Outputs(ctx, format)
	if err != nil {
		if e, ok := api.IsAPIError(err); ok && e.Status < 500 {
			return "", nil
		}
		return "", err
	}
	return f.Category, nil
}
