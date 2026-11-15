package tui

import (
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

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/zalando/go-keyring"

	"github.com/runconvert/runconvert-cli/internal/api"
	"github.com/runconvert/runconvert-cli/internal/auth"
	"github.com/runconvert/runconvert-cli/internal/intent"
)

// fakeAPI knows heic (image) and mov (video); jobs finish at once. It records
// the convert task of every job so tests can check the options sent.
type fakeAPI struct {
	mu    sync.Mutex
	tasks []map[string]any
}

func (f *fakeAPI) start(t *testing.T) {
	t.Helper()
	outputs := map[string][]map[string]any{
		"heic": {{"format": "jpg", "category": "image"}, {"format": "png", "category": "image"}, {"format": "webp", "category": "image"},
			{"format": "pdf", "category": "document", "credits": 2}, {"format": "avif", "category": "image"}, {"format": "tiff", "category": "image"},
			{"format": "bmp", "category": "image"}, {"format": "eps", "category": "vector"}, {"format": "jpg", "category": "image"}},
		"mov": {{"format": "mp4", "category": "video"}, {"format": "webm", "category": "video"}, {"format": "gif", "category": "image"}},
	}
	cats := map[string]string{"heic": "image", "mov": "video"}
	n := 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok := func(d any) { json.NewEncoder(w).Encode(map[string]any{"success": true, "data": d}) }
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/v3/operations" && q.Get("output") != "":
			if outputs[q.Get("input")] == nil {
				w.WriteHeader(404)
				w.Write([]byte(`{"success":false,"error":{"code":"INVALID_OPERATION","message":"not supported"}}`))
				return
			}
			ok(map[string]any{"engines": []any{map[string]any{"name": "imagemagick", "credits": 1, "options": []any{
				map[string]any{"name": "strip_metadata", "type": "boolean", "description": "Remove metadata.", "supported_values": []any{true, false}},
				map[string]any{"name": "image_quality", "type": "integer", "description": "Output quality.", "supported_values": []any{map[string]any{"type": "range", "min": 1, "max": 100}}},
				map[string]any{"name": "background_color", "type": "string", "description": "Fill colour.", "supported_values": []any{"white", "black"}},
				map[string]any{"name": "trimStart", "type": "string", "description": "Start time.", "supported_values": []any{map[string]any{"type": "pattern", "format": "HH:MM:SS"}}},
			}}}})
		case r.URL.Path == "/v3/operations":
			in := q.Get("input")
			if outputs[in] == nil {
				w.WriteHeader(404)
				w.Write([]byte(`{"success":false,"error":{"code":"INVALID_OPERATION","message":"not supported"}}`))
				return
			}
			ok(map[string]any{"format": in, "category": cats[in], "outputs": outputs[in]})
		case r.Method == "POST" && r.URL.Path == "/v3/jobs":
			var body struct {
				Tasks map[string]map[string]any `json:"tasks"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.tasks = append(f.tasks, body.Tasks["convert-1"])
			n++
			id := "job" + string(rune('a'+n))
			f.mu.Unlock()
			ok(map[string]any{"id": id, "status": "waiting", "tasks": []any{map[string]any{"name": "import-1", "upload_url": srv.URL + "/up/" + id}}})
		case r.Method == "PUT":
			io.Copy(io.Discard, r.Body)
		case strings.HasPrefix(r.URL.Path, "/v3/jobs/"):
			id := strings.TrimPrefix(r.URL.Path, "/v3/jobs/")
			ok(map[string]any{"id": id, "status": "finished", "tasks": []any{
				map[string]any{"name": "convert-1", "status": "finished", "percent": 100},
				map[string]any{"name": "export-1", "operation": "export/url", "status": "finished",
					"result": map[string]any{"files": []any{map[string]any{"url": srv.URL + "/dl/" + id}}}},
			}})
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			w.Write([]byte("OUT"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("RUNCONVERT_API_URL", srv.URL)
}

func setup(t *testing.T) (Model, *fakeAPI) {
	t.Helper()
	keyring.MockInit() // never touch the real system keychain
	t.Setenv("RUNCONVERT_NO_BROWSER", "1")
	f := &fakeAPI{}
	f.start(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	return New(api.New("rc_live_test", "test"), "test"), f
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func send(t *testing.T, m Model, msgs ...tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var model tea.Model
		model, cmd = m.Update(msg)
		m = model.(Model)
	}
	return m, cmd
}

// finish feeds conversion events until the run is done.
func finish(t *testing.T, m Model) Model {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for m.mode == running {
		select {
		case msg := <-m.events:
			m, _ = send(t, m, msg)
		case <-deadline:
			t.Fatal("conversion didn't finish")
		}
	}
	return m
}

func write(t *testing.T, dir string, names ...string) {
	for _, n := range names {
		p := filepath.Join(dir, n)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDropShowsEveryFormatAndADigitConverts(t *testing.T) {
	m, _ := setup(t)
	dir := t.TempDir()
	write(t, dir, "My Photo.heic", "other.heic")
	m, cmd := send(t, m, tea.PasteMsg{Content: strings.ReplaceAll(filepath.Join(dir, "My Photo.heic"), " ", `\ `)})
	if m.mode != loading || cmd == nil {
		t.Fatalf("a dropped file should load the picker, mode %v", m.mode)
	}
	m, _ = send(t, m, cmd())
	if m.mode != picking || m.sel.rows() {
		t.Fatalf("one file → the grid, mode %v", m.mode)
	}
	view := m.View().Content
	for _, want := range []string{"Popular", "1 JPG", "Image", "TIFF", "BMP", "Vector", "EPS", "8 of 8 formats", "PDF ·2cr"} {
		if !strings.Contains(view, want) {
			t.Errorf("grid should show %q:\n%s", want, view)
		}
	}
	m, _ = send(t, m, key("1"))
	m = finish(t, m)
	if b, err := os.ReadFile(filepath.Join(dir, "My Photo.jpg")); err != nil || string(b) != "OUT" {
		t.Fatalf("not saved next to the original: %v", err)
	}
	steps := m.nextSteps()
	if len(steps) != 3 || !strings.Contains(steps[2].label, "All 2 HEIC") {
		t.Fatalf("next steps: %+v", steps)
	}
}

func TestTypingFiltersTheGrid(t *testing.T) {
	m, _ := setup(t)
	f := filepath.Join(t.TempDir(), "a.heic")
	write(t, filepath.Dir(f), "a.heic")
	m, _ = send(t, m, m.loadSelection([]string{f}, false)())
	m, _ = send(t, m, key("t"), key("i"))
	if v := m.View().Content; !strings.Contains(v, "1 of 8") || !strings.Contains(v, "TIFF") {
		t.Fatalf("filter 'ti' should leave TIFF:\n%s", v)
	}
	m, _ = send(t, m, key("enter"))
	m = finish(t, m)
	if _, err := os.Stat(filepath.Join(filepath.Dir(f), "a.tiff")); err != nil {
		t.Fatal("Enter should convert to the filtered format")
	}
}

func TestOptionsScreenSendsAndRemembersOptions(t *testing.T) {
	m, api := setup(t)
	dir := t.TempDir()
	write(t, dir, "a.heic")
	m, _ = send(t, m, m.loadSelection([]string{filepath.Join(dir, "a.heic")}, false)())
	m, cmd := send(t, m, key("tab"))
	m, _ = send(t, m, cmd())
	if m.mode != optionsing || len(m.form.fields) != 4 {
		t.Fatalf("options: mode %v %+v", m.mode, m.form)
	}
	// Most-changed first: quality, then background.
	if m.form.fields[0].name != "image_quality" || m.form.fields[0].kind != "number" || m.form.fields[1].kind != "choice" {
		t.Fatalf("order/kinds: %+v", m.form.fields)
	}
	// Quality: type 80. Background: → picks white. More: strip metadata on.
	m, _ = send(t, m, key("8"), key("0"), key("down"), key("right"), key("m"), key("down"))
	if m.form.fields[m.form.at].kind != "toggle" {
		t.Fatalf("expected the toggle, at %+v", m.form.fields[m.form.at])
	}
	m, _ = send(t, m, key("space"), key("enter"))
	m = finish(t, m)
	got := api.tasks[len(api.tasks)-1]
	if got["image_quality"] != float64(80) || got["background_color"] != "white" || got["strip_metadata"] != true {
		t.Fatalf("options sent: %v", got)
	}
	// Remembered for heic → jpg next time.
	if s := loadSettings()["heic>jpg"]; s["image_quality"] == nil {
		t.Fatalf("settings not saved: %v", loadSettings())
	}
	m, _ = send(t, m, m.loadSelection([]string{filepath.Join(dir, "a.heic")}, false)())
	m.in.SetValue("")
	if p := m.planFrom(m.sel); p.batches[0].opts.EngineOptions["image_quality"] == nil || len(p.notes) != 1 {
		t.Fatalf("saved options should be used and mentioned: %+v", p)
	}
}

func TestFolderRowsDefaultsAndSubfolders(t *testing.T) {
	m, _ := setup(t)
	dir := filepath.Join(t.TempDir(), "Holiday")
	write(t, dir, "a.heic", "b.heic", "c.mov", "notes.xyz", "sub/d.heic")
	m, _ = send(t, m, m.loadSelection([]string{dir}, false)())
	s := m.sel
	if !s.rows() || len(s.kinds) != 2 || len(s.kinds[0].files) != 2 || len(s.notes) != 1 {
		t.Fatalf("rows: %+v", s)
	}
	if s.kinds[0].target(true) != "jpg" || s.kinds[1].target(true) != "skip" {
		t.Fatalf("only the biggest group starts on: %s %s", s.kinds[0].target(true), s.kinds[1].target(true))
	}
	m, _ = send(t, m, key("down"), key("right")) // videos: skip → mp4
	m, cmd := send(t, m, key("space"))           // include subfolders (reload)
	m, _ = send(t, m, cmd())
	if !m.sel.recursive || len(m.sel.kinds[0].files) != 3 || m.sel.kinds[1].target(true) != "mp4" {
		t.Fatalf("subfolders + kept choices: %+v", m.sel.kinds)
	}
	p := m.planFrom(m.sel)
	if len(p.batches) != 2 || p.batches[0].opts.OutDir != dir+" (converted)" || !p.batches[0].opts.KeepStructure {
		t.Fatalf("plan: %+v", p.batches[0].opts)
	}
}

func TestTypedCategory(t *testing.T) {
	m, _ := setup(t)
	dir := t.TempDir()
	write(t, dir, "a.heic", "b.mov", "c.mov")
	in, err := intent.Parse("all videos in " + dir + " to mp4")
	if err != nil {
		t.Fatal(err)
	}
	p, ok := m.prepare(in)().(planMsg)
	if !ok || plan(p).count() != 2 || len(p.notes) != 1 {
		t.Fatalf("only the 2 videos: %#v", p)
	}
}

func TestHomeBasics(t *testing.T) {
	m, _ := setup(t)
	m, _ = send(t, m, key("/"))
	if !strings.Contains(m.View().Content, "/formats") {
		t.Error("typing / should list the commands")
	}
	m.in.SetValue("")
	m, _ = m.submitLine("/out ~/Desktop/converted")
	if !strings.HasSuffix(m.outDir, filepath.Join("Desktop", "converted")) {
		t.Errorf("/out: %q", m.outDir)
	}
	m, _ = m.submitLine("mp4 to mp3")
	if m.waiting == nil {
		t.Error("a format pair without a file should ask which file")
	}
	for _, p := range []string{"/Users/me/a.heic", "/tmp/x"} {
		if isCommand(p) {
			t.Errorf("%s is a path", p)
		}
	}
	if len(m.history) != 2 {
		t.Errorf("history: %v", m.history)
	}
}

func TestNoKeyAsksForOne(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RUNCONVERT_NO_BROWSER", "1")
	m := New(api.New("", "test"), "test")
	if m.mode != askKey {
		t.Fatal("without a key, ask for one")
	}
	m, _ = m.submitLine("not-a-key")
	if m.mode != askKey {
		t.Fatal("a bad key keeps asking")
	}
}

// submitLine types a line and presses Enter.
func (m Model) submitLine(line string) (Model, tea.Cmd) {
	model, cmd := m.submit(line)
	return model.(Model), cmd
}

func TestDropAsFastKeystrokes(t *testing.T) {
	m, _ := setup(t)
	f := filepath.Join(t.TempDir(), "a.heic")
	write(t, filepath.Dir(f), "a.heic")
	var cmd tea.Cmd
	for _, r := range f {
		m, cmd = send(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if cmd == nil {
		t.Fatal("a burst that fills the input should schedule a drop check")
	}
	m, cmd = send(t, m, dropMsg(m.in.Value()))
	if m.mode != loading || cmd == nil {
		t.Fatalf("the burst was a drop: mode %v", m.mode)
	}
	m, _ = send(t, m, cmd())
	if m.mode != picking {
		t.Fatalf("picker: mode %v", m.mode)
	}
	// Typing slowly is not a drop.
	m.toHome()
	for _, r := range "abc.heic" {
		m.lastKey = time.Now().Add(-time.Second)
		m, _ = send(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.burstLen > 1 {
		t.Fatalf("slow typing counted as a burst: %d", m.burstLen)
	}
}

func TestSignInInsideTheApp(t *testing.T) {
	m, _ := setup(t)
	m.c.Key = ""
	m.toKeyMode()
	m, _ = send(t, m, deviceMsg(&api.DeviceCode{DeviceCode: "rcd_x", UserCode: "WXYZ-BCDF",
		VerificationURI: "https://www.runconvert.com/cli/connect", VerificationURIComplete: "https://www.runconvert.com/cli/connect?code=WXYZ-BCDF", Interval: 5, ExpiresIn: 600}))
	if m.device == nil || m.mode != askKey {
		t.Fatalf("waiting for the browser: %+v", m.device)
	}
	m, cmd := send(t, m, keyOKMsg("rc_live_from_browser"))
	if m.mode != home || m.c.Key != "rc_live_from_browser" || cmd == nil {
		t.Fatalf("signed in: mode %v key %q", m.mode, m.c.Key)
	}
	if k := auth.Key(""); k != "rc_live_from_browser" {
		t.Fatalf("the key should be saved, got %q", k)
	}
	// A second approval (browser and a pasted key) is ignored.
	m2, _ := send(t, m, keyOKMsg("rc_live_other"))
	if m2.c.Key != "rc_live_from_browser" {
		t.Fatal("a late second sign-in must not replace the key")
	}
}

func TestLogoutDoesNotOpenTheBrowser(t *testing.T) {
	m, _ := setup(t)
	m, cmd := m.submitLine("/logout")
	if m.mode != askKey || m.device != nil || m.c.Key != "" {
		t.Fatalf("signed out: mode %v device %v", m.mode, m.device)
	}
	// Only a print: no sign-in started (which would open the browser).
	if msg := cmd(); msg != nil {
		if _, started := msg.(deviceMsg); started {
			t.Fatal("logout must not start a browser sign-in")
		}
	}
	if !strings.Contains(m.hint(), "/login") {
		t.Fatalf("hint should offer /login: %q", m.hint())
	}
	// /login (not a key) starts it.
	m, cmd = m.submitLine("/login")
	if cmd == nil || m.mode != askKey {
		t.Fatal("/login should start signing in")
	}
	// /exit quits even while signed out.
	if _, cmd = m.submitLine("/exit"); cmd == nil {
		t.Fatal("/exit should quit")
	}
}

func TestSignedOutTypingIsReadableAndKeysAreHidden(t *testing.T) {
	m, _ := setup(t)
	m, _ = m.submitLine("/logout")
	m, _ = send(t, m, key("/"), key("l"), key("o"))
	if m.in.EchoMode != textinput.EchoNormal || !strings.Contains(m.View().Content, "/lo") {
		t.Fatalf("/login must be visible while typing, echo %v", m.in.EchoMode)
	}
	m.in.SetValue("")
	m, _ = send(t, m, tea.PasteMsg{Content: "rc_live_secret"})
	if m.in.EchoMode != textinput.EchoPassword || strings.Contains(m.View().Content, "rc_live_secret") {
		t.Fatal("a pasted key must be hidden")
	}
}

func TestInputBoxStaysNarrowInWideWindows(t *testing.T) {
	m, _ := setup(t)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 270, Height: 70})
	for _, line := range strings.Split(m.View().Content, "\n") {
		if w := lipgloss.Width(line); w > maxInputWidth+2 {
			t.Fatalf("a %d-column line in a 270-column window: %q", w, line)
		}
	}
}

func TestShrinkingBelowTheDrawingClears(t *testing.T) {
	m, _ := setup(t)
	m, _ = send(t, m, tea.WindowSizeMsg{Width: 200, Height: 40})
	if _, cmd := m.Update(tea.WindowSizeMsg{Width: 150, Height: 40}); cmd != nil {
		t.Fatal("shrinking while everything still fits shouldn't clear")
	}
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 50, Height: 40})
	if cmd == nil {
		t.Fatal("shrinking below the drawing should move up over the re-wrapped rows")
	}
}
