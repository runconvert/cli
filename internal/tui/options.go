package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/runconvert/cli/internal/api"
	"github.com/runconvert/cli/internal/auth"
)

// field is one option on the options screen, with a control that suits what
// the API says it accepts: a list of choices, on/off, a number range, or text.
type field struct {
	name, desc, kind string // kind: choice | toggle | number | text
	values           []any  // choices
	min, max         float64
	isInt            bool
	unit             string
	val              any    // nil = the API's default
	text             string // number/text being typed
}

type optionsForm struct {
	kindIdx  int
	from, to string
	credits  int
	fields   []field
	basic    int // the first `basic` fields are shown before "More"
	at       int
	more     bool
}

// priority puts the options people change most first ("Basic").
var priority = []string{"quality", "bitrate", "width", "height", "resolution", "background_color", "fps", "framerate", "dpi", "page", "codec"}

func rank(name string) int {
	n := strings.ToLower(name)
	for i, p := range priority {
		if strings.Contains(n, p) {
			return i
		}
	}
	return len(priority)
}

func buildFields(opts []api.Option) []field {
	var out []field
	for _, o := range opts {
		f := field{name: o.Name, desc: o.Description}
		vals, _ := o.SupportedValues.([]any)
		switch {
		case o.Type == "boolean":
			f.kind = "toggle"
		case len(vals) > 0:
			if r, ok := vals[0].(map[string]any); ok {
				switch r["type"] {
				case "range":
					f.kind = "number"
					f.min, _ = r["min"].(float64)
					f.max, _ = r["max"].(float64)
					f.unit, _ = r["unit"].(string)
					f.isInt = o.Type == "integer" || (f.min == math.Trunc(f.min) && f.max == math.Trunc(f.max))
				default:
					f.kind = "text"
					if p, _ := r["format"].(string); p != "" {
						f.unit = p
					}
				}
			} else {
				f.kind = "choice"
				f.values = vals
			}
		case o.Type == "integer" || o.Type == "number":
			f.kind, f.isInt = "number", o.Type == "integer"
		default:
			f.kind = "text"
		}
		out = append(out, f)
	}
	// Stable: most-changed first, then the API's order.
	ordered := make([]field, 0, len(out))
	for r := 0; r <= len(priority); r++ {
		for _, f := range out {
			if rank(f.name) == r {
				ordered = append(ordered, f)
			}
		}
	}
	return ordered
}

func (m Model) openOptions(kindIdx int) (tea.Model, tea.Cmd) {
	k := m.sel.kinds[kindIdx]
	to := k.target(m.sel.rows())
	if to == "" || to == "skip" {
		return m, nil
	}
	cat := m.cat
	prev := k.opts
	if prev == nil {
		prev = loadSettings()[k.format+">"+to]
	}
	m.mode = loading
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		engines, err := cat.Engines(ctx, k.format, to)
		if err != nil {
			return errMsg{err}
		}
		if len(engines) == 0 || len(engines[0].Options) == 0 {
			return errMsg{fmt.Errorf("%s → %s has no options to set", strings.ToUpper(k.format), strings.ToUpper(to))}
		}
		f := optionsForm{kindIdx: kindIdx, from: k.format, to: to, credits: engines[0].Credits, fields: buildFields(engines[0].Options)}
		f.basic = 0
		for _, fl := range f.fields {
			if rank(fl.name) < len(priority) && f.basic < 5 {
				f.basic++
			}
		}
		if f.basic == 0 {
			f.basic = min(4, len(f.fields))
		}
		for i := range f.fields {
			if v, ok := prev[f.fields[i].name]; ok {
				f.fields[i].val = v
				f.fields[i].text = fmt.Sprint(v)
			}
		}
		return formMsg(f)
	}
}

func (f *optionsForm) shown() int {
	if f.more {
		return len(f.fields)
	}
	return f.basic
}

func (f *optionsForm) values() map[string]any {
	out := map[string]any{}
	for _, fl := range f.fields {
		if fl.val != nil {
			out[fl.name] = fl.val
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (m Model) formKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.form
	fl := &f.fields[f.at]
	typing := fl.kind == "number" || fl.kind == "text"
	switch key := k.String(); key {
	case "esc":
		m.form = nil
		m.mode = picking
		return m, nil
	case "up":
		f.at = max(0, f.at-1)
		return m, nil
	case "down":
		f.at = min(f.shown()-1, f.at+1)
		return m, nil
	case "left", "right":
		step := 1
		if key == "left" {
			step = -1
		}
		fl.change(step)
		return m, nil
	case "space":
		if fl.kind == "toggle" {
			fl.change(1)
			return m, nil
		}
	case "backspace":
		if typing && fl.text != "" {
			fl.text = fl.text[:len(fl.text)-1]
			fl.parse()
		}
		return m, nil
	case "enter":
		return m.applyForm()
	}
	if len(k.Text) == 0 {
		return m, nil
	}
	if typing {
		fl.text += k.Text
		fl.parse()
		return m, nil
	}
	switch k.Text {
	case "m":
		f.more = !f.more
		if !f.more {
			f.at = min(f.at, f.basic-1)
		}
	case "r":
		for i := range f.fields {
			f.fields[i].val, f.fields[i].text = nil, ""
		}
	}
	return m, nil
}

func (fl *field) change(step int) {
	switch fl.kind {
	case "toggle":
		if fl.val == true {
			fl.val = false
		} else if fl.val == false {
			fl.val = nil
		} else {
			fl.val = true
		}
	case "choice":
		// nil (default) → each value → nil
		i := -1
		for j, v := range fl.values {
			if fmt.Sprint(v) == fmt.Sprint(fl.val) && fl.val != nil {
				i = j
			}
		}
		i += step
		switch {
		case i < -1:
			i = len(fl.values) - 1
		case i >= len(fl.values):
			i = -1
		}
		if i < 0 {
			fl.val = nil
		} else {
			fl.val = fl.values[i]
		}
	case "number":
		span := fl.max - fl.min
		inc := 1.0
		if span > 20 {
			inc = math.Max(1, math.Round(span/20))
		}
		var cur float64
		switch v := fl.val.(type) {
		case float64:
			cur = v
		case int:
			cur = float64(v)
		default:
			cur = fl.max // e.g. quality starts high
			if span == 0 {
				cur = 0
			}
			step = 0
		}
		cur += float64(step) * inc
		if span > 0 {
			cur = math.Max(fl.min, math.Min(fl.max, cur))
		}
		fl.setNumber(cur)
	}
}

func (fl *field) setNumber(v float64) {
	if fl.isInt {
		fl.val = int(math.Round(v))
	} else {
		fl.val = v
	}
	fl.text = fmt.Sprint(fl.val)
}

func (fl *field) parse() {
	if fl.text == "" {
		fl.val = nil
		return
	}
	if fl.kind == "text" {
		fl.val = fl.text
		return
	}
	v, err := strconv.ParseFloat(fl.text, 64)
	if err != nil {
		return
	}
	if fl.max > fl.min {
		v = math.Max(fl.min, math.Min(fl.max, v))
	}
	if fl.isInt {
		fl.val = int(math.Round(v))
	} else {
		fl.val = v
	}
}

func (m Model) applyForm() (tea.Model, tea.Cmd) {
	f := m.form
	vals := f.values()
	m.sel.kinds[f.kindIdx].opts = vals
	saveSettings(f.from+">"+f.to, vals)
	m.form = nil
	m.mode = picking
	if !m.sel.rows() {
		return m.runSelection()
	}
	return m, nil
}

func (m Model) formView() string {
	f := m.form
	var b strings.Builder
	k := m.sel.kinds[f.kindIdx]
	name := strings.ToUpper(k.format)
	if len(k.files) == 1 {
		name = k.files[0].Name()
	}
	changed := 0
	for _, fl := range f.fields {
		if fl.val != nil {
			changed++
		}
	}
	status := "all defaults"
	if changed > 0 {
		status = fmt.Sprintf("%d changed", changed)
	}
	fmt.Fprintf(&b, "  %s %s %s %s  %s\n", m.t.chip.Render(strings.ToUpper(f.from)), name, m.t.muted.Render("→"), m.t.accent.Render(strings.ToUpper(f.to)), m.t.muted.Render("· "+status+" · defaults are fine"))
	for i := 0; i < f.shown(); i++ {
		if i == f.basic && f.more {
			b.WriteString(m.t.muted.Render("  ─ More ─") + "\n")
		}
		fl := f.fields[i]
		cursor := "  "
		label := fmt.Sprintf("%-22s", fl.name)
		if i == f.at {
			cursor = m.t.accent.Render("› ")
			label = m.t.bold.Render(label)
		}
		fmt.Fprintf(&b, "  %s%s %s  %s\n", cursor, label, m.control(fl, i == f.at), m.t.muted.Render(fl.desc))
	}
	if !f.more && len(f.fields) > f.basic {
		b.WriteString(m.t.muted.Render(fmt.Sprintf("    + %d more options (m)", len(f.fields)-f.basic)) + "\n")
	}
	return b.String()
}

func (m Model) control(fl field, active bool) string {
	chip := func(text string, on bool) string {
		if on {
			return m.t.picked.Render(" " + text + " ")
		}
		return m.t.choice.Render(" " + text + " ")
	}
	switch fl.kind {
	case "toggle":
		switch fl.val {
		case true:
			return chip("on", true)
		case false:
			return chip("off", true)
		}
		return m.t.muted.Render("default")
	case "choice":
		parts := []string{chip("default", fl.val == nil)}
		for _, v := range fl.values {
			if len(parts) > 8 && fmt.Sprint(v) != fmt.Sprint(fl.val) {
				continue
			}
			parts = append(parts, chip(fmt.Sprint(v), fl.val != nil && fmt.Sprint(v) == fmt.Sprint(fl.val)))
		}
		if len(fl.values) > 8 {
			parts = append(parts, m.t.muted.Render(fmt.Sprintf("…%d", len(fl.values))))
		}
		return strings.Join(parts, " ")
	case "number":
		v := "default"
		if fl.val != nil {
			v = fmt.Sprint(fl.val)
		}
		if active && fl.text != "" && fmt.Sprint(fl.val) != fl.text {
			v = fl.text
		}
		rng := ""
		if fl.max > fl.min {
			rng = fmt.Sprintf(" %g–%g %s", fl.min, fl.max, fl.unit)
		}
		return "◀ " + m.t.accent.Render(v) + " ▶" + m.t.muted.Render(rng)
	}
	v := fl.text
	if v == "" {
		v = m.t.muted.Render("default")
		if fl.unit != "" {
			v = m.t.muted.Render(fl.unit)
		}
	}
	if active {
		v += m.t.accent.Render("▏")
	}
	return v
}

// ── Remembered settings: the last options used for each conversion ────────

func settingsPath() string {
	p, err := auth.Path()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "settings.json")
}

func loadSettings() map[string]map[string]any {
	out := map[string]map[string]any{}
	if b, err := os.ReadFile(settingsPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func saveSettings(pair string, vals map[string]any) {
	p := settingsPath()
	if p == "" {
		return
	}
	all := loadSettings()
	if vals == nil {
		delete(all, pair)
	} else {
		all[pair] = vals
	}
	b, _ := json.MarshalIndent(all, "", "  ")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, b, 0o600)
}
