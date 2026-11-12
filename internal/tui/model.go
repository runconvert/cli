// Package tui is `runconvert` on its own: a Claude Code-style app in the
// terminal. Drag files in (or Ctrl+O to pick), choose from every format the
// file converts to, set options if you like, and watch it convert. Typing
// sentences ("photo.heic to jpg") and /commands work too.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/runconvert/cli/internal/api"
	"github.com/runconvert/cli/internal/catalog"
	"github.com/runconvert/cli/internal/engine"
	"github.com/runconvert/cli/internal/intent"
)

type mode int

const (
	home       mode = iota
	loading         // checking files / formats
	picking         // choosing formats for dropped or picked files
	optionsing      // the options screen
	confirming      // "Found 12 files · 12 credits. Convert? (Y/n)"
	running
	askKey
	browsing // the file browser in the terminal
)

const placeholder = "drag files here · Ctrl+O to pick · or type: photo.heic to jpg"

// Model is the app's state.
type Model struct {
	c       *api.Client
	cat     *catalog.Cache
	version string
	t       theme

	in     textinput.Model
	mode   mode
	width  int
	height int
	outDir string

	history []string
	hist    int

	sel     *selection
	form    *optionsForm
	plan    *plan
	waiting *intent.Intent // asked "which file?"
	browser filepicker.Model

	tos map[int]string // output format per file index, for progress rows

	// A drop that arrives as keystrokes (no bracketed paste): characters much
	// faster than anyone types, filling the whole input.
	lastKey  time.Time
	burstLen int
	active   map[int]*progressRow
	jobIDs   map[string]bool
	cancel   context.CancelFunc
	events   chan tea.Msg
	started  time.Time
	last     *lastRun
	nextSel  int
}

// New builds the app for a client (an empty key asks for one first).
func New(c *api.Client, version string) Model {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = placeholder
	ti.CharLimit = 8192
	ti.SetWidth(92) // until the terminal reports its size
	ti.Focus()
	m := Model{c: c, cat: catalog.NewCache(c), version: version, t: newTheme(startDark()), in: ti,
		active: map[int]*progressRow{}, jobIDs: map[string]bool{}, width: 100}
	m.history = loadHistory()
	m.hist = len(m.history)
	if c.Key == "" {
		m.toKeyMode()
	}
	return m
}

// Run starts the app in the terminal.
func Run(ctx context.Context, c *api.Client, version string) error {
	_, err := tea.NewProgram(New(c, version), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (m *Model) toKeyMode() {
	m.mode = askKey
	m.in.Placeholder = "paste your API key (rc_live_…) from runconvert.com/dashboard → API keys"
	m.in.EchoMode = textinput.EchoPassword
	m.in.EchoCharacter = '•'
}

func (m *Model) toHome() {
	m.mode = home
	m.sel, m.form, m.plan = nil, nil, nil
	m.in.EchoMode = textinput.EchoNormal
	m.in.Placeholder = placeholder
	m.in.SetValue("")
}

func (m Model) Init() tea.Cmd {
	lines := []string{
		m.t.accent.Render("RunConvert") + m.t.muted.Render("  "+m.version),
		"Drag files in, press " + m.t.bold.Render("Ctrl+O") + " to pick, or type " + m.t.bold.Render("photo.heic to jpg"),
		m.t.muted.Render("Results are saved next to the originals · /out to change · /help"),
	}
	if m.mode == askKey {
		lines = append(lines, "", "First, your API key: create one at https://www.runconvert.com/dashboard → API keys.")
	}
	cmds := []tea.Cmd{tea.Println(m.t.box.Render(strings.Join(lines, "\n")))}
	if !forcedTheme() {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	return tea.Batch(cmds...)
}

// ── Messages ──────────────────────────────────────────────────────────────

type (
	errMsg   struct{ err error }
	printMsg string
	keyOKMsg string
	selMsg   selection
	formMsg  optionsForm
	planMsg  plan
	eventMsg engine.Event
	doneMsg  []engine.Result
	pickMsg  []string // files chosen in the system's file window
	dropMsg  string   // the input to check, once a fast burst of keys stops
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.t = newTheme(msg.IsDark())
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.in.SetWidth(max(20, msg.Width-8))
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.PasteMsg:
		return m.paste(msg.Content)
	case printMsg:
		return m, tea.Println(string(msg))
	case errMsg:
		if m.mode != askKey {
			m.toHome()
		}
		return m, tea.Println(m.fail(msg.err))
	case keyOKMsg:
		return m.signedIn(string(msg))
	case dropMsg:
		if m.mode == home && m.waiting == nil && m.in.Value() == string(msg) {
			if paths, ok := allPaths(string(msg)); ok {
				m.in.SetValue("")
				m.mode = loading
				return m, m.loadSelection(paths, false)
			}
		}
		return m, nil
	case pickMsg:
		m.toHome()
		if len(msg) == 0 {
			return m, nil
		}
		m.mode = loading
		return m, m.loadSelection([]string(msg), false)
	case selMsg:
		return m.gotSelection(selection(msg))
	case formMsg:
		f := optionsForm(msg)
		m.form = &f
		m.mode = optionsing
		return m, nil
	case planMsg:
		return m.gotPlan(plan(msg))
	case eventMsg:
		return m.event(engine.Event(msg))
	case doneMsg:
		return m.done([]engine.Result(msg))
	}
	var cmd tea.Cmd
	if m.mode == browsing {
		m.browser, cmd = m.browser.Update(msg) // the folder listing arrives here
		return m, cmd
	}
	m.in, cmd = m.in.Update(msg)
	return m, cmd
}

func (m Model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if s := k.String(); s == "ctrl+c" || s == "ctrl+d" {
		if m.cancel != nil {
			m.cancel()
		}
		return m, tea.Quit
	}
	switch m.mode {
	case picking:
		return m.pickKey(k)
	case optionsing:
		return m.formKey(k)
	case browsing:
		return m.browseKey(k)
	case running:
		if k.String() == "esc" {
			return m, m.stop()
		}
		return m, nil
	case loading:
		return m, nil
	case confirming:
		switch strings.ToLower(k.String()) {
		case "enter", "y":
			p := *m.plan
			return m.start(p)
		case "esc", "n":
			m.toHome()
			return m, tea.Println(m.t.muted.Render("  Cancelled."))
		}
		return m, nil
	}
	return m.homeKey(k)
}

// ── View ──────────────────────────────────────────────────────────────────

func (m Model) View() tea.View {
	var b strings.Builder
	keys := make([]int, 0, len(m.active))
	for k := range m.active {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		r := m.active[k]
		fmt.Fprintf(&b, "%s %s %s %s %s\n", m.t.accent.Render("⟳"), r.name, m.t.muted.Render("→ "+strings.ToUpper(r.to)), m.bar(r.pct), m.t.muted.Render(r.stageText()))
	}
	switch m.mode {
	case loading:
		b.WriteString(m.t.muted.Render("  Checking files…") + "\n")
	case picking:
		b.WriteString(m.selectionView())
	case optionsing:
		b.WriteString(m.formView())
		b.WriteString(m.t.muted.Render(" " + m.hint()))
		return tea.NewView(b.String())
	case browsing:
		b.WriteString(m.t.bold.Render("  Choose a file") + m.t.muted.Render("  "+tilde(m.browser.CurrentDirectory)) + "\n")
		b.WriteString(m.browser.View() + "\n")
		b.WriteString(m.t.muted.Render(" ↑↓ move · → or Enter open folder / pick file · ← back · Esc cancel"))
		return tea.NewView(b.String())
	case home:
		if steps := m.nextSteps(); len(steps) > 0 && m.in.Value() == "" {
			var chips []string
			for i, st := range steps {
				if i == m.nextSel%len(steps) {
					chips = append(chips, m.t.picked.Render(" "+st.label+" "))
				} else {
					chips = append(chips, m.t.choice.Render(" "+st.label+" "))
				}
			}
			b.WriteString("  " + strings.Join(chips, " ") + "\n")
		}
		if isCommand(m.in.Value()) {
			for _, s := range slashCommands {
				if strings.HasPrefix(s.name, m.in.Value()) {
					fmt.Fprintf(&b, "  %-10s %s\n", m.t.accent.Render(s.name), m.t.muted.Render(s.help))
				}
			}
		}
	}
	w := max(30, m.width-2)
	b.WriteString(m.t.input.Width(w).Render(m.in.View()) + "\n")
	b.WriteString(m.t.muted.Render(" " + m.hint()))
	return tea.NewView(b.String())
}

func (m Model) hint() string {
	switch m.mode {
	case running:
		return "Esc cancel · Ctrl+C quit"
	case confirming:
		return "Enter or y convert · n or Esc cancel"
	case askKey:
		return "Enter save key · Ctrl+C quit"
	case loading:
		return "Ctrl+C quit"
	case picking:
		if m.sel != nil && m.sel.rows() {
			return "↑↓ row · ← → format · type a format · Tab options · Space subfolders · Enter convert · Esc cancel"
		}
		return "1–5 popular · ↑↓←→ move · type to filter · Tab options · Enter convert · Esc cancel"
	case optionsing:
		return "↑↓ choose · ← → change · type a value · m more · r reset · Enter convert · Esc back"
	}
	if m.waiting != nil {
		return "Drag the file in, or type its path · Esc cancel"
	}
	if len(m.nextSteps()) > 0 && m.in.Value() == "" {
		return "← → then Enter for a next step · or drag in more files"
	}
	return "Drag files in · Ctrl+O pick · Tab complete · ↑↓ history · /help · Ctrl+C quit"
}

func (m Model) bar(pct int) string {
	const n = 14
	fill := min(n, max(0, pct*n/100))
	return m.t.accent.Render(strings.Repeat("█", fill)) + m.t.muted.Render(strings.Repeat("░", n-fill))
}

func (m Model) fail(err error) string {
	return m.t.err.Render("✗ ") + errText(err)
}

func errText(err error) string {
	if err == nil {
		return "failed"
	}
	if e, ok := api.IsAPIError(err); ok && e.Hint() != "" {
		return e.Error() + ". " + e.Hint()
	}
	return err.Error()
}

func tilde(p string) string {
	if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, h) {
		return "~" + strings.TrimPrefix(p, h)
	}
	return p
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return h + strings.TrimPrefix(p, "~")
		}
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
