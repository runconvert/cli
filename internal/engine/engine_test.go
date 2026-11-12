package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExpandFolders(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "a.heic"))
	touch(t, filepath.Join(dir, "B.HEIC"))
	touch(t, filepath.Join(dir, ".hidden.heic"))
	touch(t, filepath.Join(dir, "sub", "c.heic"))
	touch(t, filepath.Join(dir, ".git", "d.heic"))

	flat, err := Expand([]string{dir}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) != 2 {
		t.Fatalf("top level: got %d inputs, want 2: %+v", len(flat), flat)
	}
	for _, in := range flat {
		if in.Format != "heic" {
			t.Errorf("%s: format %q, want heic", in.Path, in.Format)
		}
	}

	deep, err := Expand([]string{dir, filepath.Join(dir, "a.heic")}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(deep) != 3 {
		t.Fatalf("recursive: got %d inputs, want 3 (no duplicates, no hidden)", len(deep))
	}
}

func TestExpandGlobAndURL(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "x.webp"))
	touch(t, filepath.Join(dir, "y.webp"))
	touch(t, filepath.Join(dir, "z.png"))
	got, err := Expand([]string{filepath.Join(dir, "*.webp"), "https://example.com/files/report.docx?dl=1"}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d inputs, want 3", len(got))
	}
	var url Input
	for _, in := range got {
		if in.URL != "" {
			url = in
		}
	}
	if url.Format != "docx" || url.Name() != "report.docx" {
		t.Errorf("url input: format %q name %q", url.Format, url.Name())
	}
	if _, err := Expand([]string{filepath.Join(dir, "*.mov")}, false, ""); err == nil {
		t.Error("a glob with no matches should be an error")
	}
}

func TestDest(t *testing.T) {
	in := Input{Path: "/p/Holiday/sub/IMG_1.heic", Base: "/p/Holiday", Rel: "sub/IMG_1.heic", Format: "heic"}
	cases := []struct {
		name string
		o    Options
		want string
	}{
		{"next to input", Options{To: "jpg"}, "/p/Holiday/IMG_1.jpg"},
		{"keep structure", Options{To: "jpg", KeepStructure: true}, "/p/Holiday/sub/IMG_1.jpg"},
		{"out dir", Options{To: "jpg", OutDir: "/out"}, "/out/IMG_1.jpg"},
		{"out dir + structure", Options{To: "jpg", OutDir: "/out", KeepStructure: true}, "/out/sub/IMG_1.jpg"},
		{"pattern", Options{To: "JPG", Pattern: "/x/{name}-{from}.{ext}"}, "/x/IMG_1-heic.jpg"},
	}
	for _, c := range cases {
		got, err := Dest(in, c.o)
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.FromSlash(c.want) {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
