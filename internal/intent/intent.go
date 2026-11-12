// Package intent turns what someone types in the interactive session
// ("photo.heic to jpg", "all webp in ~/Downloads to png 1200px") into a
// conversion. It's a small set of patterns, not AI: anything else gets a hint.
package intent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/runconvert/cli/internal/api"
	"github.com/runconvert/cli/internal/catalog"
)

// Intent is one conversion request.
type Intent struct {
	Sources   []string          // paths, globs, folders or URLs, ~ expanded
	Filter    string            // only files of this format ("all webp in …")
	Category  string            // only files of this category ("all videos in …")
	Recursive bool              // include subfolders
	From      string            // input format when no file was named ("mp4 to mp3")
	To        string            // output format
	Settings  map[string]string // bitrate, quality, width, height → value
}

// NeedsFile reports that a conversion was asked for without naming a file.
func (i Intent) NeedsFile() bool { return len(i.Sources) == 0 }

var ErrNoTarget = errors.New(`say what to convert to, e.g. "photo.heic to jpg" or "all webp in ~/Downloads to png"`)

// Tokenize splits a line like a shell: quotes group words, and outside quotes
// a backslash escapes the next character (macOS Terminal escapes spaces in
// dragged paths). Inside quotes, and on Windows, backslashes are kept.
func Tokenize(line string) []string {
	var out []string
	var cur strings.Builder
	in := false
	var quote rune
	esc := false
	for _, r := range line {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
			in = true
		case r == '\\' && quote == 0 && runtime.GOOS != "windows":
			esc = true // outside quotes on macOS/Linux: "My\ Photo.heic"
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			in = true
		case r == ' ' || r == '\t' || r == '\n':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

var (
	reFormat   = regexp.MustCompile(`^\.?[a-z0-9]{2,6}$`)
	reBitrate  = regexp.MustCompile(`^(\d{2,3})\s*(k|kb|kbps)$`)
	rePixels   = regexp.MustCompile(`^(\d{2,5})px$`)
	reSize     = regexp.MustCompile(`^(\d{2,5})x(\d{2,5})$`)
	reNumber   = regexp.MustCompile(`^\d{1,5}$`)
	fillers    = map[string]bool{"files": true, "file": true, "the": true, "my": true, "convert": true, "please": true, "from": true, "folder": true}
	targetWord = map[string]bool{"to": true, "into": true, "as": true, "→": true, "->": true}
	recursive  = map[string]bool{"recursive": true, "recursively": true, "-r": true, "subfolders": true, "subfolder": true, "including": true, "with": true, "and": true}
)

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func looksLikePath(tok string) bool {
	if strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://") {
		return true
	}
	if strings.ContainsAny(tok, `/\*?~`) {
		return true
	}
	if _, err := os.Stat(tok); err == nil {
		return true
	}
	// name.ext
	return filepath.Ext(tok) != "" && len(filepath.Ext(tok)) <= 6 && !strings.HasPrefix(tok, ".")
}

// Parse reads one line. It returns ErrNoTarget when no output format is given.
func Parse(line string) (Intent, error) {
	toks := Tokenize(strings.TrimSpace(line))
	in := Intent{Settings: map[string]string{}}
	if len(toks) == 0 {
		return in, ErrNoTarget
	}
	// The output format: the token after the last "to"/"into"/"as"/"→".
	at := -1
	for i := len(toks) - 2; i >= 0; i-- {
		if targetWord[strings.ToLower(toks[i])] && reFormat.MatchString(strings.ToLower(toks[i+1])) {
			at = i
			break
		}
	}
	if at < 0 {
		// "photo.heic jpg": paths, then a format, no "to".
		last := strings.ToLower(toks[len(toks)-1])
		if len(toks) < 2 || targetWord[last] || !reFormat.MatchString(last) || looksLikePath(toks[len(toks)-1]) {
			return in, ErrNoTarget
		}
		for _, t := range toks[:len(toks)-1] {
			if !looksLikePath(t) && !fillers[strings.ToLower(t)] {
				return in, ErrNoTarget
			}
		}
		toks = append(toks[:len(toks)-1:len(toks)-1], "to", toks[len(toks)-1])
		at = len(toks) - 2
	}
	in.To = strings.TrimPrefix(strings.ToLower(toks[at+1]), ".")

	// After the format: settings and "including subfolders".
	rest := toks[at+2:]
	for i := 0; i < len(rest); i++ {
		w := strings.ToLower(rest[i])
		next := ""
		if i+1 < len(rest) {
			next = strings.ToLower(rest[i+1])
		}
		switch {
		case recursive[w]:
			if w == "recursive" || w == "recursively" || w == "-r" || w == "subfolders" || w == "subfolder" {
				in.Recursive = true
			}
		case reBitrate.MatchString(w):
			in.Settings["bitrate"] = reBitrate.FindStringSubmatch(w)[1] + "k"
		case rePixels.MatchString(w):
			in.Settings["width"] = rePixels.FindStringSubmatch(w)[1]
		case reSize.MatchString(w):
			m := reSize.FindStringSubmatch(w)
			in.Settings["width"], in.Settings["height"] = m[1], m[2]
		case (w == "quality" || w == "q") && reNumber.MatchString(strings.TrimSuffix(next, "%")):
			in.Settings["quality"] = strings.TrimSuffix(next, "%")
			i++
		case (w == "width" || w == "w") && reNumber.MatchString(strings.TrimSuffix(next, "px")):
			in.Settings["width"] = strings.TrimSuffix(next, "px")
			i++
		case (w == "height" || w == "h") && reNumber.MatchString(strings.TrimSuffix(next, "px")):
			in.Settings["height"] = strings.TrimSuffix(next, "px")
			i++
		case (w == "bitrate" || w == "at") && reBitrate.MatchString(next):
			in.Settings["bitrate"] = reBitrate.FindStringSubmatch(next)[1] + "k"
			i++
		default:
			return in, fmt.Errorf("didn't understand %q. Settings I know: 192k, quality 80, width 1200 (or 1200px), 1920x1080, including subfolders", rest[i])
		}
	}

	// Before the format: sources, "all <fmt> in <dir>", or just "<fmt>".
	src := toks[:at]
	for i := 0; i < len(src); i++ {
		tok := src[i]
		w := strings.ToLower(tok)
		switch {
		case w == "all" || w == "every" || w == "each":
			if i+1 < len(src) && catalog.Categories[strings.ToLower(src[i+1])] != "" {
				in.Category = catalog.Categories[strings.ToLower(src[i+1])]
				i++
			} else if i+1 < len(src) && reFormat.MatchString(strings.ToLower(src[i+1])) && !looksLikePath(src[i+1]) {
				in.Filter = strings.TrimPrefix(strings.ToLower(src[i+1]), ".")
				i++
			}
		case w == "in" || w == "inside" || w == "under":
			if i+1 < len(src) {
				in.Sources = append(in.Sources, expandHome(src[i+1]))
				i++
			}
		case recursive[w] && w != "and" && w != "with" && w != "including":
			in.Recursive = true
		case catalog.Categories[w] != "" && !exists(tok):
			in.Category = catalog.Categories[w]
		case fillers[w] || targetWord[w] || w == "and" || w == "with" || w == "including":
		case looksLikePath(tok):
			in.Sources = append(in.Sources, expandHome(tok))
		case reFormat.MatchString(w):
			in.From = strings.TrimPrefix(w, ".")
		default:
			return in, fmt.Errorf("couldn't find %q. Drag the file into this window, or type its path", tok)
		}
	}
	if (in.Filter != "" || in.Category != "") && len(in.Sources) == 0 {
		cwd, _ := os.Getwd()
		in.Sources = []string{cwd}
	}
	if in.Filter != "" && in.From == "" {
		in.From = in.Filter
	}
	return in, nil
}

// Resolve maps friendly settings to the API's option names and types for a
// conversion (from GET /v3/operations?input=&output=).
func Resolve(settings map[string]string, engines []api.Engine) (map[string]any, error) {
	out := map[string]any{}
	if len(settings) == 0 || len(engines) == 0 {
		return out, nil
	}
	opts := engines[0].Options
	find := func(word string) *api.Option {
		var best *api.Option
		for i := range opts {
			n := strings.ToLower(opts[i].Name)
			if !strings.Contains(n, word) {
				continue
			}
			// Prefer audio bitrate for "bitrate", image quality for "quality".
			if best == nil || strings.Contains(n, "audio") || strings.Contains(n, "image") {
				best = &opts[i]
			}
		}
		return best
	}
	for k, v := range settings {
		o := find(k)
		if o == nil {
			return nil, fmt.Errorf("%s can't be set for this conversion", k)
		}
		switch o.Type {
		case "integer":
			n, err := strconv.Atoi(strings.TrimSuffix(v, "k"))
			if err != nil {
				return nil, fmt.Errorf("%s needs a whole number", k)
			}
			out[o.Name] = n
		case "number":
			f, err := strconv.ParseFloat(strings.TrimSuffix(v, "k"), 64)
			if err != nil {
				return nil, fmt.Errorf("%s needs a number", k)
			}
			out[o.Name] = f
		default:
			out[o.Name] = v
		}
	}
	return out, nil
}
