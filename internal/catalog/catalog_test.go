package catalog

import (
	"reflect"
	"testing"

	"github.com/runconvert/cli/internal/api"
)

func names(g Group) (out []string) {
	for _, f := range g.Formats {
		out = append(out, f.Format)
	}
	return
}

func TestGroupedShowsEveryOutputOnce(t *testing.T) {
	// PNG, as the API lists it: some formats once per engine.
	in := &api.InputFormat{Format: "png", Category: "image", Outputs: []api.Output{
		{Format: "pdf", Category: "document"}, {Format: "avif", Category: "image"}, {Format: "bmp", Category: "image"},
		{Format: "eps", Category: "vector"}, {Format: "gif", Category: "image"}, {Format: "icns", Category: "image"},
		{Format: "ico", Category: "image"}, {Format: "jpg", Category: "image"}, {Format: "png", Category: "image"},
		{Format: "ps", Category: "vector"}, {Format: "psd", Category: "image"}, {Format: "tiff", Category: "image"},
		{Format: "webp", Category: "image"}, {Format: "md", Category: "document"}, {Format: "jpg", Category: "image"},
	}}
	g := Grouped(in)
	want := map[string][]string{
		"Popular":  {"jpg", "webp", "pdf", "avif"},
		"Image":    {"bmp", "gif", "icns", "ico", "psd", "tiff"},
		"Document": {"md"},
		"Vector":   {"eps", "ps"},
	}
	order := []string{"Popular", "Image", "Document", "Vector"}
	if len(g) != len(order) {
		t.Fatalf("groups: %+v", g)
	}
	total := 0
	for i, name := range order {
		if g[i].Name != name || !reflect.DeepEqual(names(g[i]), want[name]) {
			t.Errorf("group %d = %s %v, want %s %v", i, g[i].Name, names(g[i]), name, want[name])
		}
		total += len(g[i].Formats)
	}
	if total != 13 {
		t.Errorf("every output once, without png itself: got %d, want 13", total)
	}
}

func TestTypedOptions(t *testing.T) {
	engines := []api.Engine{{Options: []api.Option{
		{Name: "audioBitrate", Type: "string"}, {Name: "sampleRate", Type: "string"},
		{Name: "volume", Type: "integer"}, {Name: "blur", Type: "number"}, {Name: "fade", Type: "boolean"},
	}}}
	got, err := TypedOptions(engines, []string{"audioBitrate=192k", "sampleRate=8000", "volume=80", "blur=1.5", "fade=true"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"audioBitrate": "192k", "sampleRate": "8000", "volume": int64(80), "blur": 1.5, "fade": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	for _, bad := range [][]string{{"nope=1"}, {"volume=loud"}, {"fade=maybe"}, {"novalue"}} {
		if _, err := TypedOptions(engines, bad); err == nil {
			t.Errorf("%v should fail", bad)
		}
	}
}
