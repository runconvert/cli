package intent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/runconvert/cli/internal/api"
)

func TestTokenizeDraggedPaths(t *testing.T) {
	cases := map[string][]string{
		`/Users/me/My\ Photo.heic to jpg`:   {"/Users/me/My Photo.heic", "to", "jpg"},
		`'/Users/me/My Photo.heic' to jpg`:  {"/Users/me/My Photo.heic", "to", "jpg"},
		`"C:\Users\me\a b.png" to webp`:     {`C:\Users\me\a b.png`, "to", "webp"},
		`  photo.heic   to   jpg  `:         {"photo.heic", "to", "jpg"},
		`a\ b.heic c.heic to png quality 8`: {"a b.heic", "c.heic", "to", "png", "quality", "8"},
	}
	for in, want := range cases {
		if got := Tokenize(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Tokenize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	cases := []struct {
		line string
		want Intent
	}{
		{"photo.heic to jpg", Intent{Sources: []string{"photo.heic"}, To: "jpg"}},
		{"photo.heic into .JPG", Intent{Sources: []string{"photo.heic"}, To: "jpg"}},
		{"convert photo.heic as png", Intent{Sources: []string{"photo.heic"}, To: "png"}},
		{`/tmp/My\ Photo.heic to jpg`, Intent{Sources: []string{"/tmp/My Photo.heic"}, To: "jpg"}},
		{"a.heic b.heic to jpg", Intent{Sources: []string{"a.heic", "b.heic"}, To: "jpg"}},
		{"~/Desktop/*.webp to png", Intent{Sources: []string{filepath.Join(home, "Desktop/*.webp")}, To: "png"}},
		{"https://example.com/r.docx to pdf", Intent{Sources: []string{"https://example.com/r.docx"}, To: "pdf"}},
		{"all webp in ~/Downloads to png", Intent{Sources: []string{filepath.Join(home, "Downloads")}, Filter: "webp", From: "webp", To: "png"}},
		{"all webp files in ~/Downloads to png", Intent{Sources: []string{filepath.Join(home, "Downloads")}, Filter: "webp", From: "webp", To: "png"}},
		{"all heic to jpg", Intent{Sources: []string{cwd}, Filter: "heic", From: "heic", To: "jpg"}},
		{"~/Pictures/Holiday to jpg including subfolders", Intent{Sources: []string{filepath.Join(home, "Pictures/Holiday")}, To: "jpg", Recursive: true}},
		{"talk.mp4 to mp3 192k", Intent{Sources: []string{"talk.mp4"}, To: "mp3", Settings: map[string]string{"bitrate": "192k"}}},
		{"talk.mp4 to mp3 at 320kbps", Intent{Sources: []string{"talk.mp4"}, To: "mp3", Settings: map[string]string{"bitrate": "320k"}}},
		{"logo.png to webp quality 80", Intent{Sources: []string{"logo.png"}, To: "webp", Settings: map[string]string{"quality": "80"}}},
		{"logo.png to webp 1200px", Intent{Sources: []string{"logo.png"}, To: "webp", Settings: map[string]string{"width": "1200"}}},
		{"logo.png to webp width 800 height 600", Intent{Sources: []string{"logo.png"}, To: "webp", Settings: map[string]string{"width": "800", "height": "600"}}},
		{"clip.mov to mp4 1920x1080", Intent{Sources: []string{"clip.mov"}, To: "mp4", Settings: map[string]string{"width": "1920", "height": "1080"}}},
		{"mp4 to mp3", Intent{From: "mp4", To: "mp3"}},
		{"photo.heic jpg", Intent{Sources: []string{"photo.heic"}, To: "jpg"}},
		{"convert to photo.heic to jpg", Intent{Sources: []string{"photo.heic"}, To: "jpg"}},
		{"all videos in ~/Downloads to mp4", Intent{Sources: []string{filepath.Join(home, "Downloads")}, Category: "video", To: "mp4"}},
		{"photos in ~/Desktop to jpg", Intent{Sources: []string{filepath.Join(home, "Desktop")}, Category: "image", To: "jpg"}},
		{"all music to mp3", Intent{Sources: []string{cwd}, Category: "audio", To: "mp3"}},
	}
	for _, c := range cases {
		got, err := Parse(c.line)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.line, err)
			continue
		}
		if c.want.Settings == nil {
			c.want.Settings = map[string]string{}
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q)\n got  %+v\n want %+v", c.line, got, c.want)
		}
	}
}

func TestParseNeedsFile(t *testing.T) {
	i, err := Parse("mp4 to mp3")
	if err != nil || !i.NeedsFile() || i.From != "mp4" {
		t.Fatalf("got %+v %v", i, err)
	}
}

func TestParseErrors(t *testing.T) {
	for _, line := range []string{"", "hello", "photo.heic", "photo.heic to", "photo.heic to jpg bananas", "notaformat to jpg"} {
		if _, err := Parse(line); err == nil {
			t.Errorf("Parse(%q) should fail with a hint", line)
		}
	}
}

func TestResolveUsesTheAPIsNamesAndTypes(t *testing.T) {
	engines := []api.Engine{{Name: "ffmpeg", Options: []api.Option{
		{Name: "videoBitrate", Type: "string"},
		{Name: "audioBitrate", Type: "string"},
		{Name: "image_quality", Type: "integer"},
		{Name: "width", Type: "integer"},
	}}}
	got, err := Resolve(map[string]string{"bitrate": "192k", "quality": "80", "width": "1200"}, engines)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"audioBitrate": "192k", "image_quality": 80, "width": 1200}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := Resolve(map[string]string{"height": "10"}, engines); err == nil {
		t.Error("an option this conversion doesn't have should be an error")
	}
}
