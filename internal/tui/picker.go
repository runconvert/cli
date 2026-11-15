package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/filepicker"
	tea "charm.land/bubbletea/v2"

	"github.com/runconvert/runconvert-cli/internal/api"
	"github.com/runconvert/runconvert-cli/internal/catalog"
	"github.com/runconvert/runconvert-cli/internal/engine"
	"github.com/runconvert/runconvert-cli/internal/intent"
)

// ── What was dropped or picked ────────────────────────────────────────────

// kind is one kind of file in the selection ("42 HEIC photos").
type kind struct {
	format, category string
	files            []engine.Input
	groups           []catalog.Group       // every output, grouped
	all              map[string]api.Output // output format → details
	g, i             int                   // grid cursor (one kind)
	choices          []string              // row choices (several kinds): popular + "skip"
	sel              int
	opts             map[string]any // set on the options screen
}

func (k kind) target(rows bool) string {
	if rows {
		return k.choices[k.sel]
	}
	if k.g < len(k.groups) && k.i < len(k.groups[k.g].Formats) {
		return k.groups[k.g].Formats[k.i].Format
	}
	return ""
}

type selection struct {
	sources   []string
	folder    bool
	recursive bool
	kinds     []kind
	at        int
	notes     []string
}

// rows: a folder or mixed files show one row per kind; one kind shows the grid.
func (s *selection) rows() bool { return s.folder || len(s.kinds) > 1 }

func (s *selection) totals() (files, credits int) {
	for _, k := range s.kinds {
		t := k.target(s.rows())
		if t == "" || t == "skip" {
			continue
		}
		files += len(k.files)
		credits += len(k.files) * max(1, k.all[t].Credits)
	}
	return
}

func (m Model) loadSelection(sources []string, recursive bool) tea.Cmd {
	cat := m.cat
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		s := selection{sources: sources, recursive: recursive}
		for _, src := range sources {
			if st, err := os.Stat(src); err == nil && st.IsDir() {
				s.folder = true
			}
		}
		inputs, err := engine.Expand(sources, recursive, "")
		if err != nil {
			return errMsg{err}
		}
		byFormat := map[string][]engine.Input{}
		var formats []string
		for _, in := range inputs {
			if _, ok := byFormat[in.Format]; !ok {
				formats = append(formats, in.Format)
			}
			byFormat[in.Format] = append(byFormat[in.Format], in)
		}
		var unknown []string
		for _, f := range formats {
			files := byFormat[f]
			out, err := cat.Outputs(ctx, f)
			if f == "" || err != nil || len(out.Outputs) == 0 {
				if e, ok := api.IsAPIError(err); err != nil && (!ok || e.Status >= 500) {
					return errMsg{err}
				}
				unknown = append(unknown, fmt.Sprintf("%d .%s", len(files), f))
				continue
			}
			k := kind{format: f, category: out.Category, files: files, groups: catalog.Grouped(out), all: map[string]api.Output{}}
			for _, o := range out.Outputs {
				if _, seen := k.all[o.Format]; !seen {
					k.all[o.Format] = o
				}
			}
			for _, g := range k.groups {
				for _, o := range g.Formats {
					if len(k.choices) < 5 {
						k.choices = append(k.choices, o.Format)
					}
				}
			}
			k.choices = append(k.choices, "skip")
			s.kinds = append(s.kinds, k)
		}
		sort.SliceStable(s.kinds, func(i, j int) bool { return len(s.kinds[i].files) > len(s.kinds[j].files) })
		// In a folder only the biggest group starts on, so nothing converts by surprise.
		if s.folder {
			for i := 1; i < len(s.kinds); i++ {
				s.kinds[i].sel = len(s.kinds[i].choices) - 1
			}
		}
		if len(unknown) > 0 {
			s.notes = append(s.notes, "  Leaving out "+strings.Join(unknown, ", ")+" (not a format RunConvert converts).")
		}
		if len(s.kinds) == 0 {
			if len(inputs) == 0 {
				return errMsg{errors.New("no files there")}
			}
			return errMsg{errors.New("none of those files can be converted")}
		}
		return selMsg(s)
	}
}

func (m Model) gotSelection(s selection) (tea.Model, tea.Cmd) {
	// Reloaded (subfolders on/off): keep the choices made.
	if prev := m.sel; prev != nil && m.mode == loading {
		for i := range s.kinds {
			for _, old := range prev.kinds {
				if old.format != s.kinds[i].format {
					continue
				}
				t := old.target(true)
				if !slices.Contains(s.kinds[i].choices, t) {
					s.kinds[i].choices = append([]string{t}, s.kinds[i].choices...)
				}
				s.kinds[i].sel = slices.Index(s.kinds[i].choices, t)
				s.kinds[i].opts = old.opts
			}
		}
		s.at = min(prev.at, len(s.kinds)-1)
	}
	m.sel = &s
	m.mode = picking
	m.in.SetValue("")
	if s.rows() {
		m.in.Placeholder = "type a format for this row (e.g. tiff), or press Enter to convert"
	} else {
		m.in.Placeholder = "type to filter formats (e.g. ti)"
	}
	return m, nil
}

// ── The grid (one kind of file) ───────────────────────────────────────────

type cell struct{ g, i int }

// visible lists the grid cells matching the filter typed in the input.
func (k kind) visible(filter string) []cell {
	filter = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(filter)), ".")
	var out []cell
	for g, grp := range k.groups {
		for i, o := range grp.Formats {
			if filter == "" || strings.Contains(o.Format, filter) {
				out = append(out, cell{g, i})
			}
		}
	}
	return out
}

func (m Model) pickKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := m.sel
	key := k.String()
	if key == "esc" {
		if m.in.Value() != "" {
			m.in.SetValue("")
			return m, nil
		}
		m.toHome()
		return m, tea.Println(m.t.muted.Render("  Cancelled."))
	}
	if s.rows() {
		return m.rowsKey(k)
	}
	kd := &s.kinds[0]
	vis := kd.visible(m.in.Value())
	pos := slices.Index(vis, cell{kd.g, kd.i})
	switch key {
	case "left", "right":
		if len(vis) == 0 {
			return m, nil
		}
		if pos < 0 {
			pos = 0
		} else if key == "right" {
			pos = (pos + 1) % len(vis)
		} else {
			pos = (pos + len(vis) - 1) % len(vis)
		}
		kd.g, kd.i = vis[pos].g, vis[pos].i
		return m, nil
	case "up", "down":
		// The nearest visible cell in the group above or below.
		step := 1
		if key == "up" {
			step = -1
		}
		for g := kd.g + step; g >= 0 && g < len(kd.groups); g += step {
			best := -1
			for _, c := range vis {
				if c.g == g && (best < 0 || abs(c.i-kd.i) < abs(best-kd.i)) {
					best = c.i
				}
			}
			if best >= 0 {
				kd.g, kd.i = g, best
				break
			}
		}
		return m, nil
	case "tab":
		return m.openOptions(0)
	case "enter":
		if len(vis) == 0 {
			return m, tea.Println(m.t.muted.Render(fmt.Sprintf("  %s can't become %q. Clear the filter to see all.", strings.ToUpper(kd.format), m.in.Value())))
		}
		if pos < 0 {
			kd.g, kd.i = vis[0].g, vis[0].i
		}
		return m.runSelection()
	}
	// 1–5 pick a popular format and convert at once.
	if m.in.Value() == "" && len(k.Text) == 1 && k.Text[0] >= '1' && k.Text[0] <= '9' && len(kd.groups) > 0 && kd.groups[0].Name == "Popular" {
		if i := int(k.Text[0] - '1'); i < len(kd.groups[0].Formats) {
			kd.g, kd.i = 0, i
			return m.runSelection()
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.in, cmd = m.in.Update(k) // typing filters
	if vis := kd.visible(m.in.Value()); len(vis) > 0 && !slices.Contains(vis, cell{kd.g, kd.i}) {
		kd.g, kd.i = vis[0].g, vis[0].i
	}
	return m, cmd
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ── Rows (a folder, or several kinds of files) ────────────────────────────

func (m Model) rowsKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := m.sel
	r := &s.kinds[s.at]
	typed := strings.TrimSpace(m.in.Value())
	switch k.String() {
	case "left":
		r.sel = (r.sel + len(r.choices) - 1) % len(r.choices)
		return m, nil
	case "right":
		r.sel = (r.sel + 1) % len(r.choices)
		return m, nil
	case "up":
		s.at = max(0, s.at-1)
		return m, nil
	case "down":
		s.at = min(len(s.kinds)-1, s.at+1)
		return m, nil
	case "space":
		if s.folder && typed == "" {
			m.mode = loading
			return m, m.loadSelection(s.sources, !s.recursive)
		}
	case "tab":
		if r.target(true) == "skip" {
			return m, tea.Println(m.t.muted.Render("  Pick a format for this row first (← →)."))
		}
		return m.openOptions(s.at)
	case "enter":
		if typed == "" {
			return m.runSelection()
		}
		f := strings.TrimPrefix(strings.ToLower(typed), ".")
		if _, ok := r.all[f]; !ok {
			return m, tea.Println(m.t.muted.Render(fmt.Sprintf("  %s can't become %s. See /formats %s", strings.ToUpper(r.format), strings.ToUpper(f), r.format)))
		}
		if !slices.Contains(r.choices, f) {
			r.choices = append([]string{f}, r.choices...)
		}
		r.sel = slices.Index(r.choices, f)
		m.in.SetValue("")
		return m, nil
	}
	var cmd tea.Cmd
	m.in, cmd = m.in.Update(k)
	return m, cmd
}

// ── Drawing the selection ─────────────────────────────────────────────────

func (m Model) selectionView() string {
	s := m.sel
	var b strings.Builder
	files := 0
	for _, k := range s.kinds {
		files += len(k.files)
	}
	label, what := "FILES", fmt.Sprintf("%d files", files)
	switch {
	case s.folder && len(s.sources) == 1:
		label, what = "FOLDER", tilde(s.sources[0])+m.t.muted.Render(fmt.Sprintf(" · %s", plural(files, "file")))
	case !s.rows() && files == 1:
		f := s.kinds[0].files[0]
		label, what = strings.ToUpper(s.kinds[0].format), f.Name()
		if st, err := os.Stat(f.Path); err == nil {
			what += m.t.muted.Render(" · " + human(st.Size()) + " · " + tilde(filepath.Dir(f.Path)))
		}
	case !s.rows():
		label = strings.ToUpper(s.kinds[0].format)
	}
	b.WriteString("  " + m.t.chip.Render(label) + " " + what + "\n")
	for _, n := range s.notes {
		b.WriteString(m.t.muted.Render(n) + "\n")
	}

	if !s.rows() {
		k := s.kinds[0]
		vis := k.visible(m.in.Value())
		n := 0
		for _, g := range k.groups {
			n += len(g.Formats)
		}
		fmt.Fprintf(&b, "  %s %s\n", m.t.bold.Render("Convert to"), m.t.muted.Render(fmt.Sprintf("· %d of %d formats", len(vis), n)))
		for gi, g := range k.groups {
			var chips []string
			for i, o := range g.Formats {
				if !slices.Contains(vis, cell{gi, i}) {
					continue
				}
				text := strings.ToUpper(o.Format)
				if g.Name == "Popular" && m.in.Value() == "" {
					text = fmt.Sprintf("%d %s", i+1, text)
				}
				if o.Credits > 1 {
					text += fmt.Sprintf(" ·%dcr", o.Credits)
				}
				if gi == k.g && i == k.i {
					chips = append(chips, m.t.picked.Render(" "+text+" "))
				} else {
					chips = append(chips, m.t.choice.Render(" "+text+" "))
				}
			}
			if len(chips) > 0 {
				fmt.Fprintf(&b, "  %s %s\n", m.t.muted.Render(fmt.Sprintf("%-11s", g.Name)), strings.Join(chips, " "))
			}
		}
		if len(vis) == 0 {
			b.WriteString(m.t.muted.Render(fmt.Sprintf("  %s can't become %q.", strings.ToUpper(k.format), m.in.Value())) + "\n")
		}
		if k.opts != nil {
			b.WriteString(m.t.muted.Render("  Options: "+optsSummary(k.opts)) + "\n")
		}
		return b.String()
	}

	b.WriteString("  " + m.t.bold.Render(fmt.Sprintf("Found %d kinds of files. Convert each to:", len(s.kinds))) + "\n")
	for i, k := range s.kinds {
		mark := m.t.muted.Render("○ ")
		if i == s.at {
			mark = m.t.accent.Render("● ")
		}
		var chips []string
		for j, c := range k.choices {
			text := strings.ToUpper(c)
			if c == "skip" {
				text = "skip"
			}
			if j == k.sel {
				chips = append(chips, m.t.picked.Render(" "+text+" "))
			} else {
				chips = append(chips, m.t.choice.Render(" "+text+" "))
			}
		}
		extra := ""
		if k.opts != nil {
			extra = m.t.muted.Render("  " + optsSummary(k.opts))
		}
		fmt.Fprintf(&b, "  %s%-16s%s%s\n", mark, fmt.Sprintf("%d %s", len(k.files), strings.ToUpper(k.format)), strings.Join(chips, " "), extra)
	}
	n, credits := s.totals()
	sub := ""
	if s.folder {
		sub = "  ☐ include subfolders (Space)"
		if s.recursive {
			sub = "  ☑ include subfolders (Space)"
		}
	}
	b.WriteString(m.t.muted.Render(fmt.Sprintf("  %s · %s%s", plural(n, "file"), plural(credits, "credit"), sub)) + "\n")
	return b.String()
}

func optsSummary(o map[string]any) string {
	var parts []string
	for k, v := range o {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// ── From choices to a run ─────────────────────────────────────────────────

type batch struct {
	inputs []engine.Input
	opts   engine.Options
}

type plan struct {
	batches []batch
	credits int
	notes   []string
}

func (p plan) count() (n int) {
	for _, b := range p.batches {
		n += len(b.inputs)
	}
	return
}

func (m Model) planFrom(s *selection) plan {
	var p plan
	rows := s.rows()
	saved := loadSettings()
	for _, k := range s.kinds {
		t := k.target(rows)
		if t == "" || t == "skip" {
			continue
		}
		o := engine.Options{To: t, OutDir: m.outDir, Parallel: 3, KeepStructure: s.recursive, EngineOptions: k.opts}
		if o.EngineOptions == nil {
			if prev := saved[k.format+">"+t]; len(prev) > 0 {
				o.EngineOptions = prev
				p.notes = append(p.notes, fmt.Sprintf("  %s → %s with your saved options: %s", strings.ToUpper(k.format), strings.ToUpper(t), optsSummary(prev)))
			}
		}
		if o.OutDir == "" && s.folder && len(s.sources) == 1 {
			o.OutDir = strings.TrimRight(s.sources[0], string(filepath.Separator)) + " (converted)"
			o.KeepStructure = true
		}
		p.batches = append(p.batches, batch{inputs: k.files, opts: o})
		p.credits += len(k.files) * max(1, k.all[t].Credits)
	}
	return p
}

func (m Model) runSelection() (tea.Model, tea.Cmd) {
	p := m.planFrom(m.sel)
	if len(p.batches) == 0 {
		return m, tea.Println(m.t.muted.Render("  Everything is set to skip. Choose a format with ← →."))
	}
	var parts []string
	for _, b := range p.batches {
		parts = append(parts, fmt.Sprintf("%s → %s", strings.ToUpper(b.inputs[0].Format), strings.ToUpper(b.opts.To)))
	}
	echo := tea.Println(m.t.muted.Render("› ") + strings.Join(parts, ", ") + m.t.muted.Render(fmt.Sprintf("  (%s)", plural(p.count(), "file"))))
	cmds := []tea.Cmd{echo}
	for _, n := range p.notes {
		cmds = append(cmds, tea.Println(m.t.muted.Render(n)))
	}
	m.sel = nil
	m.in.SetValue("")
	mm, start := m.start(p)
	return mm, tea.Sequence(append(cmds, start)...)
}

// ── Running ───────────────────────────────────────────────────────────────

type progressRow struct {
	name, to, stage string
	pct             int
	start           time.Time
}

func (r *progressRow) stageText() string {
	switch r.stage {
	case "upload":
		return fmt.Sprintf("uploading %d%%", r.pct)
	case "convert":
		return fmt.Sprintf("converting %d%%", r.pct)
	case "download":
		return "saving"
	}
	return "starting"
}

type lastRun struct {
	sources []string
	folder  bool
	to      string
	outputs []string
}

func (m Model) start(p plan) (Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.mode = running
	m.plan = nil
	m.started = time.Now()
	m.active = map[int]*progressRow{}
	m.jobIDs = map[string]bool{}
	ch := make(chan tea.Msg, 256)
	m.events = ch
	l := &lastRun{}
	tos := map[int]string{}
	idx := 0
	for _, b := range p.batches {
		l.to = b.opts.To
		for _, in := range b.inputs {
			l.sources = append(l.sources, in.Path+in.URL)
			tos[idx] = b.opts.To
			idx++
		}
	}
	m.last = l
	c := m.c
	go func() {
		var all []engine.Result
		off := 0
		for _, b := range p.batches {
			o := off
			res := engine.All(ctx, c, b.inputs, b.opts, func(e engine.Event) { e.Index += o; ch <- eventMsg(e) })
			all = append(all, res...)
			off += len(b.inputs)
			if ctx.Err() != nil {
				break
			}
		}
		ch <- doneMsg(all)
	}()
	m.tos = tos
	return m, wait(ch)
}

func wait(ch chan tea.Msg) tea.Cmd { return func() tea.Msg { return <-ch } }

func (m Model) stop() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	c, ids := m.c, m.jobIDs
	t := m.t
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for id := range ids {
			_ = c.CancelJob(ctx, id)
		}
		return printMsg(t.muted.Render("  Stopped. Conversions in progress were cancelled."))
	}
}

func (m Model) event(e engine.Event) (tea.Model, tea.Cmd) {
	next := wait(m.events)
	if e.JobID != "" {
		m.jobIDs[e.JobID] = true
	}
	row := m.active[e.Index]
	if row == nil {
		row = &progressRow{name: e.Input.Name(), to: m.tos[e.Index], start: time.Now()}
		m.active[e.Index] = row
	}
	switch e.Stage {
	case "upload", "convert", "download":
		row.stage, row.pct = e.Stage, e.Percent
		return m, next
	}
	delete(m.active, e.Index)
	took := time.Since(row.start).Round(100 * time.Millisecond)
	var line string
	switch e.Stage {
	case "done":
		line = m.t.ok.Render("✓ ") + row.name + m.t.muted.Render(" → ") + tilde(e.Dest) + m.t.muted.Render("  "+took.String())
	case "skip":
		line = m.t.muted.Render("• " + row.name + " skipped: " + tilde(e.Dest) + " already exists")
	default:
		line = m.t.err.Render("✗ ") + row.name + m.t.muted.Render(": ") + errText(e.Err)
	}
	return m, tea.Sequence(tea.Println(line), next)
}

func (m Model) done(res []engine.Result) (tea.Model, tea.Cmd) {
	m.cancel = nil
	m.toHome()
	m.nextSel = 0
	ok, failed := 0, 0
	for _, r := range res {
		if r.Error != "" {
			failed++
		} else {
			ok++
			if r.Output != "" && m.last != nil {
				m.last.outputs = append(m.last.outputs, r.Output)
			}
		}
	}
	if m.last != nil {
		if len(m.last.outputs) == 0 {
			m.last = nil
		} else if len(m.last.sources) > 1 {
			m.last.folder = true
		}
	}
	if len(res) <= 1 && failed == 0 {
		return m, m.fetchMe() // credits changed
	}
	sum := fmt.Sprintf("%d converted", ok)
	if failed > 0 {
		sum += fmt.Sprintf(", %d failed", failed)
	}
	return m, tea.Batch(tea.Println(m.t.muted.Render("  "+sum+" in "+time.Since(m.started).Round(100*time.Millisecond).String())), m.fetchMe())
}

// ── Next steps ────────────────────────────────────────────────────────────

type nextStep struct {
	label string
	run   func(Model) (tea.Model, tea.Cmd)
}

func (m Model) nextSteps() []nextStep {
	l := m.last
	if l == nil || len(l.outputs) == 0 || m.mode != home {
		return nil
	}
	steps := []nextStep{
		{"Open folder", func(m Model) (tea.Model, tea.Cmd) {
			m.last = nil
			return m, openFolder(filepath.Dir(l.outputs[0]), m.t)
		}},
		{"Convert again", func(m Model) (tea.Model, tea.Cmd) {
			m.last = nil
			m.mode = loading
			return m, m.loadSelection(l.sources, false)
		}},
	}
	if len(l.sources) == 1 && !l.folder && !engine.IsURL(l.sources[0]) {
		dir, ext := filepath.Dir(l.sources[0]), strings.ToLower(filepath.Ext(l.sources[0]))
		if matches, _ := filepath.Glob(filepath.Join(dir, "*"+ext)); len(matches) > 1 {
			from := strings.TrimPrefix(ext, ".")
			steps = append(steps, nextStep{fmt.Sprintf("All %d %s in %s → %s", len(matches), strings.ToUpper(from), filepath.Base(dir), strings.ToUpper(l.to)),
				func(m Model) (tea.Model, tea.Cmd) {
					m.last = nil
					m.mode = loading
					return m, m.prepare(intent.Intent{Sources: []string{dir}, Filter: from, From: from, To: l.to})
				}})
		}
	}
	return steps
}

func openFolder(dir string, t theme) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", dir)
		case "windows":
			cmd = exec.Command("explorer", dir)
		default:
			cmd = exec.Command("xdg-open", dir)
		}
		if err := cmd.Start(); err != nil {
			return printMsg(t.muted.Render("  Saved in " + tilde(dir)))
		}
		return printMsg(t.muted.Render("  Opened " + tilde(dir)))
	}
}

// ── Picking files: the system's window, or a browser in the terminal ─────

// nativePicker opens the system's file window, or returns nil where there is
// none (SSH, servers, Linux without a desktop).
func nativePicker() *exec.Cmd {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("osascript",
			"-e", `set fs to choose file with prompt "Choose files to convert" with multiple selections allowed`,
			"-e", `set out to ""`,
			"-e", `repeat with f in fs`, "-e", `set out to out & POSIX path of f & linefeed`, "-e", `end repeat`,
			"-e", `return out`)
	case "windows":
		return exec.Command("powershell", "-NoProfile", "-STA", "-Command",
			`Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.OpenFileDialog; $d.Multiselect = $true; $d.Title = 'Choose files to convert'; if ($d.ShowDialog() -eq 'OK') { $d.FileNames -join "`+"`n"+`" }`)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return nil
		}
		if p, err := exec.LookPath("zenity"); err == nil {
			return exec.Command(p, "--file-selection", "--multiple", "--separator=\n", "--title=Choose files to convert")
		}
		if p, err := exec.LookPath("kdialog"); err == nil {
			return exec.Command(p, "--getopenfilename", ".", "--multiple", "--separate-output")
		}
	}
	return nil
}

func (m Model) openFiles(terminal bool) (tea.Model, tea.Cmd) {
	if cmd := nativePicker(); cmd != nil && !terminal {
		return m, tea.Sequence(tea.Println(m.t.muted.Render("  Choose files in the window that opened…")), func() tea.Msg {
			out, err := cmd.Output()
			if err != nil {
				return pickMsg(nil) // cancelled
			}
			var files []string
			for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					files = append(files, l)
				}
			}
			return pickMsg(files)
		})
	}
	cwd, _ := os.Getwd()
	fp := filepicker.New()
	fp.CurrentDirectory = cwd
	fp.AutoHeight = false
	fp.SetHeight(max(6, min(16, m.height-10)))
	fp.ShowPermissions = false
	fp.ShowSize = true
	fp.Cursor = "›"
	m.browser = fp
	m.mode = browsing
	return m, fp.Init()
}

func (m Model) browseKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.toHome()
		return m, nil
	}
	var cmd tea.Cmd
	m.browser, cmd = m.browser.Update(k)
	if ok, path := m.browser.DidSelectFile(k); ok {
		m.toHome()
		m.mode = loading
		return m, m.loadSelection([]string{path}, false)
	}
	return m, cmd
}

// ── /formats and /jobs ────────────────────────────────────────────────────

func formatsText(c *api.Client, cat *catalog.Cache, t theme, args []string) tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if len(args) == 0 {
		cats, all, err := c.Catalog(ctx, "")
		if err != nil {
			return errMsg{err}
		}
		count := map[string]int{}
		for _, f := range all {
			count[f.Category]++
		}
		var b strings.Builder
		fmt.Fprintf(&b, "  %d input formats in %d categories:\n", len(all), len(cats))
		for _, c := range cats {
			fmt.Fprintf(&b, "    %-14s %s\n", c, t.muted.Render(plural(count[c], "format")))
		}
		b.WriteString(t.muted.Render("  /formats video lists one · /formats png shows what a format becomes"))
		return printMsg(b.String())
	}
	first := strings.TrimPrefix(strings.ToLower(args[0]), ".")
	if name := catalog.Categories[first]; name != "" && len(args) == 1 {
		_, list, err := c.Catalog(ctx, name)
		if err != nil {
			return errMsg{err}
		}
		var names []string
		for _, f := range list {
			names = append(names, f.Format)
		}
		return printMsg(fmt.Sprintf("  %s (%d): %s", t.bold.Render(name), len(names), strings.Join(names, " ")))
	}
	if len(args) == 1 {
		f, err := cat.Outputs(ctx, first)
		if err != nil {
			return errMsg{err}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "  %s (%s) converts to:\n", t.bold.Render(strings.ToUpper(first)), f.Category)
		for _, g := range catalog.Grouped(f) {
			var names []string
			for _, o := range g.Formats {
				names = append(names, o.Format)
			}
			fmt.Fprintf(&b, "    %-10s %s\n", g.Name, strings.Join(names, " "))
		}
		return printMsg(strings.TrimRight(b.String(), "\n"))
	}
	to := strings.TrimPrefix(strings.ToLower(args[1]), ".")
	engines, err := cat.Engines(ctx, first, to)
	if err != nil {
		return errMsg{err}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  %s → %s · %s. Options (Tab on the format picker):\n", strings.ToUpper(first), strings.ToUpper(to), plural(engines[0].Credits, "credit"))
	for _, o := range engines[0].Options {
		fmt.Fprintf(&b, "    %-22s %s\n", o.Name, t.muted.Render(o.Description))
	}
	return printMsg(strings.TrimRight(b.String(), "\n"))
}

func jobsText(c *api.Client, t theme) tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	js, err := c.ListJobs(ctx, "", 10)
	if err != nil {
		return errMsg{err}
	}
	if len(js) == 0 {
		return printMsg(t.muted.Render("  No conversions yet."))
	}
	var b strings.Builder
	for _, j := range js {
		mark := t.ok.Render("✓")
		if j.Status == "error" {
			mark = t.err.Render("✗")
		} else if j.Status != "finished" {
			mark = t.accent.Render("⟳")
		}
		fmt.Fprintf(&b, "  %s %s  %-10s %s\n", mark, j.CreatedAt.Local().Format("Jan 2 15:04"), j.Status, t.muted.Render(j.ID))
	}
	return printMsg(strings.TrimRight(b.String(), "\n"))
}
