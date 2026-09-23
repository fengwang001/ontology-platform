// Package lines splits byte strings into lines while preserving line endings.
package lines

// Line is one input line: Text excludes the terminator; NL is "\n", "\r\n"
// converted to "\n" terminator preservation, or "" for a final unterminated line.
type Line struct {
	Text string // content without line ending
	NL   string // original ending: "\n", "\r\n", or ""
}

// Split cuts s into Lines. Empty input yields no lines.
func Split(s string) []Line {
	if s == "" {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		text := s[start:i]
		nl := "\n"
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
			nl = "\r\n"
		}
		out = append(out, Line{Text: text, NL: nl})
		start = i + 1
	}
	if start < len(s) {
		out = append(out, Line{Text: s[start:], NL: ""})
	}
	return out
}

// Join rebuilds the original byte string.
func Join(ls []Line) string {
	var b []byte
	for _, l := range ls {
		if l.NL == "\r\n" {
			b = append(b, l.Text...)
			b = append(b, '\r', '\n')
		} else {
			b = append(b, l.Text...)
			b = append(b, l.NL...)
		}
	}
	return string(b)
}
