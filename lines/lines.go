// Package lines splits byte strings into logical lines, preserving each
// line's original terminator ("\n", "\r\n", or none for the final line).
package lines

// Line is one logical line. Content excludes the terminator; EOL is the
// original terminator ("\n", "\r\n", or nil); Raw is the original bytes.
type Line struct {
	Content []byte
	EOL     []byte
	Raw     []byte
}

// Split cuts s into lines. The empty string yields no lines. A trailing
// "\r\n" or "\n" belongs to the preceding line; a final line without a
// terminator is reported with EOL == nil.
func Split(s []byte) []Line {
	if len(s) == 0 {
		return nil
	}
	var out []Line
	for i := 0; i < len(s); {
		j := i
		for j < len(s) && s[j] != '\n' {
			j++
		}
		end := j
		eol := []byte(nil)
		if j < len(s) { // s[j] == '\n'
			if j > i && s[j-1] == '\r' {
				end = j - 1
				eol = []byte("\r\n")
			} else {
				eol = []byte("\n")
			}
			j++
		}
		content := s[i:end]
		raw := s[i:j]
		out = append(out, Line{Content: content, EOL: eol, Raw: raw})
		i = j
	}
	return out
}

// Join concatenates lines back into the exact original byte string.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Raw)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Raw...)
	}
	return out
}

// Make builds a line from content and an EOL ("\n", "\r\n", or nil).
func Make(content, eol []byte) Line {
	raw := make([]byte, 0, len(content)+len(eol))
	raw = append(raw, content...)
	raw = append(raw, eol...)
	return Line{Content: content, EOL: eol, Raw: raw}
}
