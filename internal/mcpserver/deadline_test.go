package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeAPI finishes jobs whose source URL contains "fast" and leaves the rest processing.
func fakeAPI(t *testing.T) *httptest.Server {
	t.Helper()
	jobs := map[string]bool{} // id → fast
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		write := func(id string, fast bool) {
			status, export := "processing", map[string]any{"name": "export-1", "operation": "export/url", "status": "waiting"}
			if fast {
				status = "finished"
				export["status"] = "finished"
				export["result"] = map[string]any{"files": []map[string]any{{"filename": "a.png", "url": "https://cdn.test/a.png"}}}
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
				"id": id, "status": status,
				"tasks": []any{map[string]any{"name": "convert-1", "status": status, "percent": 50}, export},
			}})
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/v3/jobs":
			var body struct {
				Tasks map[string]map[string]any `json:"tasks"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			src, _ := body.Tasks["import-1"]["source_url"].(string)
			id := "job-" + map[bool]string{true: "fast", false: "slow"}[strings.Contains(src, "fast")]
			jobs[id] = strings.Contains(src, "fast")
			write(id, jobs[id])
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v3/jobs/"):
			id := strings.TrimPrefix(r.URL.Path, "/v3/jobs/")
			write(id, jobs[id])
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestRemoteConvertAnswersBeforeTheProxyLimit(t *testing.T) {
	api := fakeAPI(t)
	defer api.Close()
	t.Setenv("RUNCONVERT_API_URL", api.URL)
	old := RemoteWait
	RemoteWait = 1500 * time.Millisecond
	defer func() { RemoteWait = old }()

	cs := connectRemote(t)
	start := time.Now()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "convert", Arguments: map[string]any{
		"sources": []string{"https://files.test/fast.webp", "https://files.test/slow.webp"}, "to": "png",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("took %s; should return soon after RemoteWait", took)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	var out ConvertOut
	b, _ := json.Marshal(res.StructuredContent)
	json.Unmarshal(b, &out)
	if out.Converted != 1 || out.Pending != 1 || out.Note == "" {
		t.Fatalf("want 1 converted + 1 pending with a note, got %+v", out)
	}
	for _, r := range out.Results {
		switch r.JobID {
		case "job-fast":
			if r.URL != "https://cdn.test/a.png" {
				t.Errorf("fast job URL = %q", r.URL)
			}
		case "job-slow":
			if r.Status != "processing" || r.Error != "" {
				t.Errorf("slow job: %+v", r)
			}
		}
	}
}
