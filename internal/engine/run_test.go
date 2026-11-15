package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/runconvert/runconvert-cli/internal/api"
	"github.com/runconvert/runconvert-cli/internal/catalog"
)

// fakeAPI: heic and mov are known (image, video); jobs finish at once unless
// the source URL says "slow"; downloads return "OUT".
func fakeAPI(t *testing.T) (*api.Client, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var uploads []string
	n := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok := func(d any) { json.NewEncoder(w).Encode(map[string]any{"success": true, "data": d}) }
		switch {
		case r.URL.Path == "/v3/operations":
			cat := map[string]string{"heic": "image", "mov": "video"}[r.URL.Query().Get("input")]
			if cat == "" {
				w.WriteHeader(404)
				json.NewEncoder(w).Encode(map[string]any{"success": false, "error": map[string]any{"code": "INVALID_OPERATION", "message": "not supported"}})
				return
			}
			ok(map[string]any{"format": r.URL.Query().Get("input"), "category": cat, "outputs": []any{}})
		case r.Method == "POST" && r.URL.Path == "/v3/jobs":
			var body struct {
				Tasks map[string]map[string]any `json:"tasks"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			n++
			id := "job" + string(rune('a'+n))
			if src, _ := body.Tasks["import-1"]["source_url"].(string); strings.Contains(src, "slow") {
				id = "slow" + id
			}
			mu.Unlock()
			ok(map[string]any{"id": id, "status": "waiting", "tasks": []any{map[string]any{"name": "import-1", "upload_url": srv.URL + "/up/" + id}}})
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/up/"):
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			uploads = append(uploads, r.URL.Query().Get("filename")+"="+string(b))
			mu.Unlock()
		case strings.HasPrefix(r.URL.Path, "/v3/jobs/"):
			id := strings.TrimPrefix(r.URL.Path, "/v3/jobs/")
			if strings.HasPrefix(id, "slow") {
				ok(map[string]any{"id": id, "status": "processing", "tasks": []any{map[string]any{"name": "convert-1", "status": "processing", "percent": 40}}})
				return
			}
			ok(map[string]any{"id": id, "status": "finished", "tasks": []any{
				map[string]any{"name": "convert-1", "status": "finished", "percent": 100},
				map[string]any{"name": "export-1", "operation": "export/url", "status": "finished",
					"result": map[string]any{"files": []any{map[string]any{"filename": "x", "url": srv.URL + "/dl/" + id}}}},
			}})
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			w.Write([]byte("OUT"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("RUNCONVERT_API_URL", srv.URL)
	return api.New("rc_live_test", "test"), &uploads
}

func TestAllUploadsConvertsAndSaves(t *testing.T) {
	c, uploads := fakeAPI(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.heic"), []byte("A"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "b.heic"), []byte("B"), 0o644)
	inputs, err := Expand([]string{dir}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	var events []string
	res := All(context.Background(), c, inputs, Options{To: "jpg", OutDir: out, KeepStructure: true, Parallel: 2}, func(e Event) {
		if e.Stage == "done" {
			events = append(events, e.Input.Name())
		}
	})
	for _, r := range res {
		if r.Error != "" {
			t.Fatalf("%s: %s", r.Source, r.Error)
		}
	}
	for _, p := range []string{"a.jpg", "sub/b.jpg"} {
		if b, err := os.ReadFile(filepath.Join(out, p)); err != nil || string(b) != "OUT" {
			t.Errorf("%s not saved: %v", p, err)
		}
	}
	if len(*uploads) != 2 || len(events) != 2 {
		t.Errorf("uploads %v, done events %v", *uploads, events)
	}
	// A second run skips what exists.
	res = All(context.Background(), c, inputs, Options{To: "jpg", OutDir: out, KeepStructure: true}, func(Event) {})
	if res[0].Output == "" || len(*uploads) != 2 {
		t.Error("existing outputs should be skipped without uploading")
	}
}

func TestDeadlineReturnsRunningJobs(t *testing.T) {
	c, _ := fakeAPI(t)
	start := time.Now()
	r := One(context.Background(), c, Input{URL: "https://x.test/slow.heic", Format: "heic"},
		Options{To: "jpg", Deadline: time.Now().Add(800 * time.Millisecond)}, false, 0, func(Event) {})
	if r.Status != "processing" || r.JobID == "" || r.Error != "" {
		t.Fatalf("got %+v", r)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("should return soon after the deadline")
	}
}

func TestOnlyCategory(t *testing.T) {
	c, _ := fakeAPI(t)
	in := []Input{{Format: "heic"}, {Format: "mov"}, {Format: "txt"}, {Format: "mov"}}
	kept, other, err := OnlyCategory(context.Background(), catalog.NewCache(c), in, "video")
	if err != nil || len(kept) != 2 || other != 2 {
		t.Fatalf("kept %d other %d err %v", len(kept), other, err)
	}
}
