package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/runconvert/cli/internal/auth"
	"github.com/runconvert/cli/internal/engine"
	"github.com/runconvert/cli/internal/intent"
)

const confirmOver = 5 // a typed request for more files than this asks first

var slashCommands = []struct{ name, help string }{
	{"/help", "what you can do"},
	{"/open", "pick files in a window (Ctrl+O)"},
	{"/browse", "pick a file inside the terminal (servers, SSH)"},
	{"/formats", "categories; /formats video; /formats png; /formats heic jpg"},
	{"/out", "where results go: /out ~/Desktop/converted · /out next"},
	{"/jobs", "your recent conversions"},
	{"/login", "use another API key"},
	{"/logout", "remove the saved key"},
	{"/clear", "clear the screen"},
	{"/exit", "quit (Ctrl+C)"},
}

func (m Model) homeKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	// Next steps after a conversion: ← → and Enter while the input is empty.
	if steps := m.nextSteps(); len(steps) > 0 && m.mode == home && m.in.Value() == "" {
		switch s {
		case "left":
			m.nextSel = (m.nextSel + len(steps) - 1) % len(steps)
			return m, nil
		case "right":
			m.nextSel = (m.nextSel + 1) % len(steps)
			return m, nil
		case "enter":
			return steps[m.nextSel%len(steps)].run(m)
		case "esc":
			m.last = nil
			return m, nil
		}
	}
	switch s {
	case "ctrl+o":
		if m.mode == home {
			return m.openFiles(false)
		}
	case "esc":
		m.waiting = nil
		m.in.SetValue("")
		return m, nil
	case "up":
		if m.mode == home && m.hist > 0 {
			m.hist--
			m.in.SetValue(m.history[m.hist])
			m.in.CursorEnd()
		}
		return m, nil
	case "down":
		if m.mode == home && m.hist < len(m.history) {
			m.hist++
			if m.hist == len(m.history) {
				m.in.SetValue("")
			} else {
				m.in.SetValue(m.history[m.hist])
			}
			m.in.CursorEnd()
		}
		return m, nil
	case "tab":
		if m.mode == home {
			return m, m.complete()
		}
		return m, nil
	case "enter":
		line := strings.TrimSpace(m.in.Value())
		m.in.SetValue("")
		return m.submit(line)
	}
	var cmd tea.Cmd
	m.in, cmd = m.in.Update(k)
	m.last = nil // typing dismisses the next steps
	// A drop without bracketed paste arrives as keystrokes a few
	// microseconds apart. When the whole input came in such a burst, check it
	// once the burst stops.
	now := time.Now()
	if k.Text != "" && now.Sub(m.lastKey) < 15*time.Millisecond {
		m.burstLen += len(k.Text)
	} else {
		m.burstLen = len(k.Text)
	}
	m.lastKey = now
	if v := m.in.Value(); m.burstLen >= 6 && m.burstLen >= len(v)-1 && m.mode == home && m.waiting == nil {
		return m, tea.Batch(cmd, tea.Tick(60*time.Millisecond, func(time.Time) tea.Msg { return dropMsg(v) }))
	}
	return m, cmd
}

// paste: a drop (or pasted paths) opens the format choices at once.
func (m Model) paste(text string) (tea.Model, tea.Cmd) {
	if m.mode == home && m.waiting == nil && m.in.Value() == "" {
		if paths, ok := allPaths(text); ok {
			m.last = nil
			m.mode = loading
			return m, m.loadSelection(paths, false)
		}
	}
	if m.mode == home || m.mode == askKey || m.mode == picking {
		m.in.SetValue(m.in.Value() + text)
		m.in.CursorEnd()
	}
	return m, nil
}

// allPaths reports whether text is only existing files/folders or URLs.
func allPaths(text string) ([]string, bool) {
	toks := intent.Tokenize(strings.TrimSpace(text))
	if len(toks) == 0 {
		return nil, false
	}
	var out []string
	for _, t := range toks {
		p := expandHome(t)
		if !engine.IsURL(p) && !exists(p) {
			return nil, false
		}
		out = append(out, p)
	}
	return out, true
}

func (m Model) submit(line string) (tea.Model, tea.Cmd) {
	if m.mode == askKey {
		return m.checkKey(line)
	}
	if line == "" {
		return m, nil
	}
	m.remember(line)
	echo := tea.Println(m.t.muted.Render("› ") + line)

	if first := strings.Fields(line)[0]; isCommand(first) && !exists(first) {
		mm, cmd := m.slash(line)
		return mm, tea.Sequence(echo, cmd)
	}
	if m.waiting == nil {
		if paths, ok := allPaths(line); ok {
			m.mode = loading
			return m, tea.Sequence(echo, m.loadSelection(paths, false))
		}
	}
	var in intent.Intent
	if m.waiting != nil {
		in = *m.waiting
		m.waiting = nil
		for _, t := range intent.Tokenize(line) {
			in.Sources = append(in.Sources, expandHome(t))
		}
	} else {
		var err error
		if in, err = intent.Parse(line); err != nil {
			return m, tea.Sequence(echo, tea.Println(m.t.muted.Render("  "+err.Error())))
		}
		if in.NeedsFile() {
			m.waiting = &in
			return m, tea.Sequence(echo, tea.Println(fmt.Sprintf("  Which %s file? Drag it in, or type its path.", strings.ToUpper(in.From))))
		}
	}
	m.mode = loading
	return m, tea.Sequence(echo, m.prepare(in))
}

// prepare turns a typed request into a plan: files found, formats checked,
// settings mapped to the API's option names.
func (m Model) prepare(in intent.Intent) tea.Cmd {
	cat, outDir := m.cat, m.outDir
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		inputs, err := engine.Expand(in.Sources, in.Recursive, in.From)
		if err != nil {
			return errMsg{err}
		}
		var notes []string
		if in.Filter != "" {
			kept := inputs[:0]
			for _, i := range inputs {
				if i.Format == in.Filter {
					kept = append(kept, i)
				}
			}
			inputs = kept
		}
		if in.Category != "" {
			var other int
			if inputs, other, err = engine.OnlyCategory(ctx, cat, inputs, in.Category); err != nil {
				return errMsg{err}
			}
			if other > 0 && len(inputs) > 0 {
				notes = append(notes, fmt.Sprintf("  Only %s files: leaving out %s.", in.Category, plural(other, "other file")))
			}
		}
		if len(inputs) == 0 {
			switch {
			case in.Category != "":
				return errMsg{fmt.Errorf("no %s files there", in.Category)}
			case in.Filter != "":
				return errMsg{fmt.Errorf("no .%s files there", in.Filter)}
			}
			return errMsg{errors.New("no files found")}
		}
		named := map[string]bool{}
		for _, s := range in.Sources {
			if abs, err := filepath.Abs(s); err == nil {
				if st, err := os.Stat(abs); err == nil && !st.IsDir() {
					named[abs] = true
				}
			}
		}
		credits := map[string]int{} // format → credits, -1 = can't become the target
		var options map[string]any
		for _, i := range inputs {
			if _, seen := credits[i.Format]; seen {
				continue
			}
			engines, err := cat.Engines(ctx, i.Format, in.To)
			if err != nil || len(engines) == 0 {
				credits[i.Format] = -1
				continue
			}
			credits[i.Format] = engines[0].Credits
			if options == nil && len(in.Settings) > 0 {
				if options, err = intent.Resolve(in.Settings, engines); err != nil {
					return errMsg{err}
				}
			}
		}
		total, skipped := 0, map[string]int{}
		kept := inputs[:0]
		for _, i := range inputs {
			if credits[i.Format] < 0 {
				if i.Path != "" && !named[i.Path] {
					skipped[i.Format]++
					continue
				}
				return errMsg{fmt.Errorf("%s can't be converted to %s. See /formats %s", strings.ToUpper(i.Format), strings.ToUpper(in.To), i.Format)}
			}
			total += credits[i.Format]
			kept = append(kept, i)
		}
		if len(skipped) > 0 {
			var parts []string
			for f, n := range skipped {
				parts = append(parts, fmt.Sprintf("%d .%s", n, f))
			}
			sort.Strings(parts)
			notes = append(notes, fmt.Sprintf("  Skipping %s (can't become %s).", strings.Join(parts, ", "), strings.ToUpper(in.To)))
		}
		if len(kept) == 0 {
			return errMsg{fmt.Errorf("none of those files can be converted to %s", strings.ToUpper(in.To))}
		}
		return planMsg{credits: total, notes: notes, batches: []batch{{inputs: kept,
			opts: engine.Options{To: in.To, OutDir: outDir, EngineOptions: options, Parallel: 3, KeepStructure: in.Recursive}}}}
	}
}

func (m Model) gotPlan(p plan) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, n := range p.notes {
		cmds = append(cmds, tea.Println(m.t.muted.Render(n)))
	}
	if p.count() > confirmOver {
		m.plan = &p
		m.mode = confirming
		cmds = append(cmds, tea.Println(fmt.Sprintf("  Found %s · %s. Convert them? %s",
			m.t.bold.Render(plural(p.count(), "file")), plural(p.credits, "credit"), m.t.muted.Render("(Y/n)"))))
		return m, tea.Sequence(cmds...)
	}
	mm, start := m.start(p)
	return mm, tea.Sequence(append(cmds, start)...)
}

// ── Sign-in (browser sign-in replaces this in a later phase) ──────────────

func (m Model) checkKey(line string) (tea.Model, tea.Cmd) {
	if !strings.HasPrefix(line, "rc_live_") {
		return m, tea.Println(m.t.err.Render("✗ ") + "That isn't a RunConvert API key (they start with rc_live_).")
	}
	c := *m.c
	c.Key = line
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := c.ListJobs(ctx, "", 1); err != nil {
			return errMsg{err}
		}
		return keyOKMsg(line)
	}
}

func (m Model) signedIn(key string) (tea.Model, tea.Cmd) {
	where, err := auth.SaveKey(key)
	m.c.Key = key
	m.toHome()
	if err != nil {
		return m, tea.Println(m.t.ok.Render("✓ Signed in") + m.t.muted.Render(" (not saved: "+err.Error()+")"))
	}
	return m, tea.Println(m.t.ok.Render("✓ Signed in.") + m.t.muted.Render(" Key saved in "+where+". Now convert something great."))
}

// ── /commands ─────────────────────────────────────────────────────────────

// isCommand: "/" and letters, not a path like /Users/me/a.heic.
func isCommand(s string) bool {
	if !strings.HasPrefix(s, "/") || strings.ContainsAny(s[1:], "/ .") {
		return false
	}
	for _, r := range s[1:] {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '?' {
			return false
		}
	}
	return true
}

func (m Model) slash(line string) (tea.Model, tea.Cmd) {
	f := strings.Fields(line)
	cmd, args := strings.ToLower(f[0]), f[1:]
	c, cat := m.c, m.cat
	switch cmd {
	case "/help", "/?":
		return m, tea.Println(m.helpText())
	case "/exit", "/quit", "/q":
		return m, tea.Quit
	case "/clear":
		return m, tea.ClearScreen
	case "/open":
		return m.openFiles(false)
	case "/browse":
		return m.openFiles(true)
	case "/out":
		if len(args) == 0 {
			return m, tea.Println("  Results go " + m.whereText())
		}
		if a := strings.Join(args, " "); a == "next" || a == "default" {
			m.outDir = ""
		} else {
			p, err := filepath.Abs(expandHome(intent.Tokenize(a)[0]))
			if err != nil {
				return m, tea.Println(m.fail(err))
			}
			m.outDir = p
		}
		return m, tea.Println(m.t.ok.Render("✓ ") + "Results go " + m.whereText())
	case "/login":
		m.toKeyMode()
		return m, tea.Println("  Paste your API key (rc_live_…).")
	case "/logout":
		_ = auth.Remove()
		m.c.Key = ""
		m.toKeyMode()
		return m, tea.Println(m.t.ok.Render("✓ ") + "Signed out. Paste a key to continue, or Ctrl+C to quit.")
	case "/formats":
		t := m.t
		return m, func() tea.Msg { return formatsText(c, cat, t, args) }
	case "/jobs":
		t := m.t
		return m, func() tea.Msg { return jobsText(c, t) }
	}
	return m, tea.Println(m.t.muted.Render("  Unknown command. ") + "/help" + m.t.muted.Render(" lists them."))
}

func (m Model) whereText() string {
	if m.outDir == "" {
		return "next to the original files."
	}
	return "to " + tilde(m.outDir) + "."
}

func (m Model) helpText() string {
	ex := [][2]string{
		{"(drag files or a folder in)", "pick a format, no typing"},
		{"Ctrl+O", "pick files in a window"},
		{"photo.heic to jpg", "or type it"},
		{"all webp in ~/Downloads to png", "every .webp in a folder"},
		{"all videos in ~/Movies to mp4", "a category: photos, music, documents…"},
		{"~/Pictures/Trip to jpg including subfolders", "a folder and its subfolders"},
		{"talk.mp4 to mp3 192k", "with a setting"},
		{"https://example.com/a.docx to pdf", "a file from a link"},
	}
	var b strings.Builder
	b.WriteString(m.t.bold.Render("  Convert:") + "\n")
	for _, e := range ex {
		fmt.Fprintf(&b, "    %-46s %s\n", e[0], m.t.muted.Render(e[1]))
	}
	b.WriteString("\n" + m.t.bold.Render("  Commands:") + "\n")
	for _, s := range slashCommands {
		fmt.Fprintf(&b, "    %-10s %s\n", s.name, m.t.muted.Render(s.help))
	}
	b.WriteString("\n" + m.t.muted.Render("  Tab completes paths and commands · ↑↓ history · Esc cancels · Ctrl+C quits"))
	return b.String()
}

// ── Tab completion ────────────────────────────────────────────────────────

func (m *Model) complete() tea.Cmd {
	v := m.in.Value()
	if isCommand(v) {
		var hits []string
		for _, s := range slashCommands {
			if strings.HasPrefix(s.name, v) {
				hits = append(hits, s.name)
			}
		}
		if len(hits) == 1 {
			m.in.SetValue(hits[0] + " ")
			m.in.CursorEnd()
		}
		return nil
	}
	cut := strings.LastIndex(v, " ")
	for cut > 0 && v[cut-1] == '\\' {
		cut = strings.LastIndex(v[:cut-1], " ")
	}
	head, word := v[:cut+1], v[cut+1:]
	plain := strings.ReplaceAll(word, `\ `, " ")
	matches, _ := filepath.Glob(expandHome(plain) + "*")
	if len(matches) == 0 {
		return nil
	}
	sort.Strings(matches)
	common := matches[0]
	for _, mt := range matches[1:] {
		for !strings.HasPrefix(mt, common) {
			common = common[:len(common)-1]
		}
	}
	if len(matches) == 1 {
		if st, err := os.Stat(common); err == nil && st.IsDir() {
			common += string(filepath.Separator)
		}
	}
	if strings.HasPrefix(plain, "~") {
		if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(common, h) {
			common = "~" + strings.TrimPrefix(common, h)
		}
	}
	m.in.SetValue(head + strings.ReplaceAll(common, " ", `\ `))
	m.in.CursorEnd()
	if len(matches) > 1 {
		var names []string
		for i, mt := range matches {
			if i == 24 {
				names = append(names, fmt.Sprintf("… %d more", len(matches)-i))
				break
			}
			names = append(names, filepath.Base(mt))
		}
		return tea.Println(m.t.muted.Render("  " + strings.Join(names, "   ")))
	}
	return nil
}

// ── History ───────────────────────────────────────────────────────────────

func historyPath() string {
	p, err := auth.Path()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "history")
}

func loadHistory() []string {
	b, err := os.ReadFile(historyPath())
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (m *Model) remember(line string) {
	if n := len(m.history); n == 0 || m.history[n-1] != line {
		m.history = append(m.history, line)
	}
	if len(m.history) > 500 {
		m.history = m.history[len(m.history)-500:]
	}
	m.hist = len(m.history)
	if p := historyPath(); p != "" {
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte(strings.Join(m.history, "\n")+"\n"), 0o600)
	}
}
