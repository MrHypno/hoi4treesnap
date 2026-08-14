package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Localisation handling.
//
// The yml files are read with a small line scanner instead of the old PEG
// grammar. Besides being far faster it copes with everything modern HOI4 loc
// throws at it: [Root.GetNameDefCap], [?var|+0], £icon£, §Y colour codes and
// escaped quotes inside the value.

// hoi4Languages maps the label shown in the UI to the loc language tag.
// These are exactly the languages the base game ships, i.e. the folders under
// its localisation directory.
var hoi4Languages = []struct {
	Label string
	Tag   string
}{
	{"English", "l_english"},
	{"Brazilian Portuguese", "l_braz_por"},
	{"French", "l_french"},
	{"German", "l_german"},
	{"Japanese", "l_japanese"},
	{"Korean", "l_korean"},
	{"Polish", "l_polish"},
	{"Russian", "l_russian"},
	{"Simplified Chinese", "l_simp_chinese"},
	{"Spanish", "l_spanish"},
}

func languageLabel(tag string) string {
	for _, l := range hoi4Languages {
		if l.Tag == tag {
			return l.Label
		}
	}
	return tag
}

func languageTag(label string) string {
	for _, l := range hoi4Languages {
		if l.Label == label {
			return l.Tag
		}
	}
	return "l_english"
}

func stripBOM(s string) string {
	if strings.HasPrefix(s, string(utf8bom)) {
		return s[len(utf8bom):]
	}
	return s
}

// cp1252High maps the 0x80-0x9F range of Windows-1252 to Unicode. Older mod
// files are still saved in that encoding and used to come out as garbage.
var cp1252High = [32]rune{
	0x20AC, 0xFFFD, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0xFFFD, 0x017D, 0xFFFD,
	0xFFFD, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0xFFFD, 0x017E, 0x0178,
}

// readFileText reads a file and makes sure the result is valid UTF-8.
func readFileText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	b = bytes.TrimPrefix(b, utf8bom)
	if utf8.Valid(b) {
		return string(b), nil
	}
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/4)
	for _, c := range b {
		switch {
		case c < 0x80:
			sb.WriteByte(c)
		case c < 0xA0:
			sb.WriteRune(cp1252High[c-0x80])
		default:
			sb.WriteRune(rune(c))
		}
	}
	return sb.String(), nil
}

// parseLocFile pulls key/value pairs out of one yml file. It returns the
// language declared in the file so the caller can skip the wrong ones.
func parseLocFile(path string) (lang string, pairs map[string]string, err error) {
	src, err := readFileText(path)
	if err != nil {
		return "", nil, err
	}

	pairs = make(map[string]string)
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Language header, e.g. "l_english:".
		if lang == "" && strings.HasPrefix(trimmed, "l_") {
			if i := strings.IndexByte(trimmed, ':'); i > 0 && strings.TrimSpace(trimmed[i+1:]) == "" {
				lang = trimmed[:i]
				continue
			}
		}

		key, value, ok := splitLocLine(trimmed)
		if !ok {
			continue
		}
		pairs[key] = value
	}
	return lang, pairs, nil
}

// splitLocLine parses `key:0 "value"`. The version number is optional and the
// value may contain escaped quotes, so the closing quote is found by scanning
// rather than by taking the last one on the line.
func splitLocLine(line string) (key, value string, ok bool) {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:colon])
	if key == "" {
		return "", "", false
	}

	rest := line[colon+1:]
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	rest = rest[i:]

	q := strings.IndexByte(rest, '"')
	if q < 0 {
		// Some files leave the value unquoted. Take what is left of the line.
		v := strings.TrimSpace(rest)
		if h := strings.IndexByte(v, '#'); h >= 0 {
			v = strings.TrimSpace(v[:h])
		}
		if v == "" {
			return "", "", false
		}
		return key, v, true
	}

	body := rest[q+1:]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' && i+1 < len(body) {
			b.WriteByte(c)
			b.WriteByte(body[i+1])
			i++
			continue
		}
		if c == '"' {
			return key, b.String(), true
		}
		b.WriteByte(c)
	}
	// Unterminated string, keep what we found.
	return key, b.String(), true
}

// loadLocalisation fills locMap for the selected language. Later search paths
// override earlier ones, and a localisation/replace folder overrides its own
// mod, which is how the game resolves them.
func loadLocalisation(paths []string, language string, progress func(float64)) {
	step := 0.0
	if len(paths) > 0 {
		step = 1.0 / float64(len(paths))
	}

	for _, root := range paths {
		var files []string
		for _, sub := range []string{"localisation", "localization"} {
			files = append(files, walkFiles(filepath.Join(root, sub), ".yml")...)
		}
		if len(files) == 0 {
			if progress != nil {
				progress(step)
			}
			continue
		}

		// replace/ wins inside the same mod, so parse it last.
		var normal, replace []string
		for _, f := range files {
			if isInReplaceFolder(f) {
				replace = append(replace, f)
			} else {
				normal = append(normal, f)
			}
		}

		for _, group := range [][]string{normal, replace} {
			results := parseFilesConcurrently(group, func(path string) (map[string]string, bool) {
				// Skip other languages by filename first, it saves reading
				// roughly seven eighths of the vanilla localisation folder.
				if !locFileMayMatch(path, language) {
					return nil, false
				}
				lang, pairs, err := parseLocFile(path)
				if err != nil {
					warnf("localisation file could not be read: %v: %v", path, err)
					return nil, false
				}
				if lang != "" && lang != language {
					return nil, false
				}
				return pairs, true
			})
			for _, pairs := range results {
				for k, v := range pairs {
					locMap[k] = v
				}
			}
		}

		if progress != nil {
			progress(step)
		}
	}
}

func isInReplaceFolder(p string) bool {
	return strings.Contains(strings.ToLower(filepath.ToSlash(p)), "/replace/")
}

// locFileMayMatch guesses from the path whether a file holds the wanted
// language. Files that carry no language marker at all are always read.
func locFileMayMatch(path, language string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	if strings.Contains(lower, "_"+language+".") || strings.Contains(lower, "/"+language+"/") {
		return true
	}
	for _, l := range hoi4Languages {
		if strings.Contains(lower, "_"+l.Tag+".") || strings.Contains(lower, "/"+l.Tag+"/") {
			return false
		}
	}
	return true
}

// loadScriptedLocalisation reads common/scripted_localisation so that focus
// titles built from [GetSomething] can be shown as real text.
//
// A defined_text lists several text blocks guarded by triggers. Since there is
// no country to evaluate them against, the unconditional entry is used, which
// is the base wording the focus falls back to in game.
func loadScriptedLocalisation(paths []string) {
	for _, root := range paths {
		files := walkFiles(filepath.Join(root, "common", "scripted_localisation"), ".txt")
		results := parseFilesConcurrently(files, func(path string) (map[string]string, bool) {
			src, err := readFileText(path)
			if err != nil {
				warnf("scripted localisation could not be read: %v: %v", path, err)
				return nil, false
			}
			out := make(map[string]string)
			collectDefinedText(ParsePDX(src, filepath.Base(path)), out)
			return out, true
		})
		for _, m := range results {
			for k, v := range m {
				scriptedLoc[k] = v
			}
		}
	}
}

func collectDefinedText(blk *PBlock, out map[string]string) {
	if blk == nil {
		return
	}
	for _, n := range blk.Nodes {
		if n.Block == nil {
			continue
		}
		if !strings.EqualFold(n.Key, "defined_text") {
			collectDefinedText(n.Block, out)
			continue
		}
		name := n.Block.Str("name")
		if name == "" {
			continue
		}
		texts := n.Block.GetAll("text")
		if len(texts) == 0 {
			continue
		}
		chosen := ""
		for _, t := range texts {
			if t.Block == nil {
				continue
			}
			key := t.Block.Str("localization_key")
			if key == "" {
				key = t.Block.Str("localisation_key")
			}
			if key == "" {
				continue
			}
			if chosen == "" {
				chosen = key
			}
			if !t.Block.Has("trigger") {
				// Unconditional entry, this is the base text.
				chosen = key
				break
			}
		}
		if chosen != "" {
			out[name] = chosen
		}
	}
}

const maxLocDepth = 8

// resolveText turns a localisation key into the string to draw. Nested keys,
// scripted localisation commands and icon markers are expanded; colour codes
// are left in place for the renderer to interpret.
func resolveText(key string) string {
	if key == "" {
		return ""
	}
	raw, ok := locMap[key]
	if !ok {
		return key
	}
	return expandLoc(raw, 0)
}

func expandLoc(s string, depth int) string {
	if depth > maxLocDepth {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '[':
			end := matchBracket(s, i)
			if end < 0 {
				b.WriteByte(c)
				continue
			}
			b.WriteString(resolveCommand(s[i+1:end], depth))
			i = end

		case '$':
			end := strings.IndexByte(s[i+1:], '$')
			if end < 0 {
				b.WriteByte(c)
				continue
			}
			name := s[i+1 : i+1+end]
			if v, ok := locMap[name]; ok {
				b.WriteString(expandLoc(v, depth+1))
			}
			i += end + 1

		case '\\':
			if i+1 < len(s) {
				switch s[i+1] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte(' ')
				case '"':
					b.WriteByte('"')
				case '\\':
					b.WriteByte('\\')
				default:
					b.WriteByte(s[i+1])
				}
				i++
				continue
			}
			b.WriteByte(c)

		default:
			// £icon£ markers cannot be drawn in a focus box, drop them.
			if c == 0xC2 && i+1 < len(s) && s[i+1] == 0xA3 {
				if end := strings.Index(s[i+2:], "£"); end >= 0 {
					i += 2 + end + 1
					continue
				}
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}

func matchBracket(s string, start int) int {
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// resolveCommand expands the contents of a [ ... ] loc command.
func resolveCommand(cmd string, depth int) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}

	// [?variable|format] prints a runtime number, there is nothing to show.
	if strings.HasPrefix(cmd, "?") {
		return ""
	}
	// Drop the |formatting suffix.
	if i := strings.IndexByte(cmd, '|'); i >= 0 {
		cmd = cmd[:i]
	}
	// Drop scope prefixes such as Root. or From.From.
	if i := strings.LastIndexByte(cmd, '.'); i >= 0 {
		cmd = cmd[i+1:]
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}

	if key, ok := scriptedLoc[cmd]; ok {
		if v, ok := locMap[key]; ok {
			return expandLoc(v, depth+1)
		}
		return ""
	}
	// A plain key referenced from loc, e.g. [SOME_KEY].
	if v, ok := locMap[cmd]; ok {
		return expandLoc(v, depth+1)
	}
	// Unknown engine command such as [GetDateText]; printing the raw string
	// would be worse than printing nothing.
	return ""
}
