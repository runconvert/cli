package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/runconvert/cli/internal/api"
)

func TestKeyFromRequest(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer rc_live_abc")
	if k := KeyFromRequest(r); k != "rc_live_abc" {
		t.Errorf("header: got %q", k)
	}
	if k := KeyFromRequest(httptest.NewRequest("POST", "/mcp/rc_live_xyz", nil)); k != "rc_live_xyz" {
		t.Errorf("path: got %q", k)
	}
	if k := KeyFromRequest(httptest.NewRequest("POST", "/mcp/something", nil)); k != "" {
		t.Errorf("non-key path: got %q", k)
	}
}

func TestRemoteNeedsKey(t *testing.T) {
	srv := httptest.NewServer(Handler("test", "test", nil))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
	if h := resp.Header.Get("WWW-Authenticate"); !strings.Contains(h, `resource_metadata="https://mcp.runconvert.com/.well-known/oauth-protected-resource"`) {
		t.Fatalf("401 must point AI apps at the OAuth metadata, got %q", h)
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	srv := httptest.NewServer(Handler("test", "test", nil))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m["resource"] != "https://mcp.runconvert.com/mcp" {
		t.Errorf("resource = %v", m["resource"])
	}
	if as, _ := m["authorization_servers"].([]any); len(as) != 1 || as[0] != "https://api.runconvert.com" {
		t.Errorf("authorization_servers = %v", m["authorization_servers"])
	}
}

// keyTransport adds the API key header, as an MCP client would.
type keyTransport struct{ key string }

func (k keyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+k.key)
	return http.DefaultTransport.RoundTrip(r)
}

func connectRemote(t *testing.T) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(Handler("test", "test", nil))
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: keyTransport{"rc_live_test"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestRemoteToolsHaveAnnotations(t *testing.T) {
	cs := connectRemote(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 4 {
		t.Fatalf("got %d tools, want 4", len(res.Tools))
	}
	for _, tool := range res.Tools {
		if tool.Annotations == nil || tool.Annotations.Title == "" {
			t.Errorf("%s: directories require a title annotation", tool.Name)
		}
		if !tool.Annotations.ReadOnlyHint && tool.Annotations.DestructiveHint == nil {
			t.Errorf("%s: a write tool needs destructiveHint", tool.Name)
		}
	}
}

func TestRemoteRejectsLocalPaths(t *testing.T) {
	cs := connectRemote(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "convert",
		Arguments: map[string]any{"sources": []string{"/etc/passwd"}, "to": "pdf"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("a local path on the remote server must be refused")
	}
}

// TestLiveListFormats calls the real public formats API; skipped with -short.
func TestLiveListFormats(t *testing.T) {
	if testing.Short() {
		t.Skip("needs network")
	}
	ct, st := mcp.NewInMemoryTransports()
	s := New(api.New("", "test"), Local, "test")
	go s.Run(context.Background(), st)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_formats", Arguments: map[string]any{"from": "mp4", "to": "mp3"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out["engine"] != "ffmpeg" {
		t.Errorf("engine = %v, want ffmpeg", out["engine"])
	}
	call := func(args map[string]any) map[string]any {
		r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_formats", Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("list_formats %v: %v %+v", args, err, r)
		}
		m, _ := r.StructuredContent.(map[string]any)
		return m
	}
	if cats, _ := call(map[string]any{})["categories"].([]any); len(cats) < 10 {
		t.Errorf("categories: %v", cats)
	}
	if f, _ := call(map[string]any{"from": "video"})["formats"].([]any); len(f) < 10 {
		t.Errorf("video formats: %d", len(f))
	}
	outs, _ := call(map[string]any{"from": "png"})["outputs"].([]any)
	seen := map[any]bool{}
	for _, o := range outs {
		f := o.(map[string]any)["format"]
		if seen[f] {
			t.Errorf("%v listed twice", f)
		}
		seen[f] = true
	}
	if len(outs) < 10 || outs[0].(map[string]any)["group"] != "Popular" {
		t.Errorf("png outputs: %v", outs)
	}
}
