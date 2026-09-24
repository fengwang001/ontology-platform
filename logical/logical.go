// Package logical joins physical lines of a .properties stream into logical lines.
package logical

import "sort"

// Segment describes one physical line participating in a logical line.
type Segment struct {
	PhysicalLine int  // 1-based physical line number
	Stripped     int  // leading whitespace bytes removed (0 for the first line)
	Start        int  // byte offset in the logical text where content begins
	Contributes  bool // false for swallowed whitespace-only continuation lines
}

// Line is one assembled logical line plus position bookkeeping.
type Line struct {
	Text     string
	Blank    bool
	Comment  bool
	Segments []Segment
}

// Position maps a byte offset in Text to a physical 1-based line/column.
func (l Line) Position(offset int) (line, col int) {
	i := sort.Search(len(l.Segments), func(i int) bool {
		return l.Segments[i].Start > offset
	}) - 1
	for i >= 0 && !l.Segments[i].Contributes {
		i--
	}
	if i < 0 {
		return l.Segments[0].PhysicalLine, 1
	}
	s := l.Segments[i]
	return s.PhysicalLine, s.Stripped + (offset - s.Start) + 1
}

// Reader counts every byte examined while assembling logical lines.
type Reader struct {
	examined int64
}

// Examined reports the total number of byte examinations so far.
func (r *Reader) Examined() int64 { return r.examined }

// Read assembles all logical lines from data.
func (r *Reader) Read(data []byte) []Line {
	var lines []Line
	var seg []Segment
	var body []byte
	active, blank, comment := false, false, false

	finish := func() {
		lines = append(lines, Line{Text: string(body), Blank: blank,
			Comment: comment, Segments: seg})
		body, seg, active = nil, nil, false
	}

	phys, start := 1, 0
	for {
		if start >= len(data) {
			break
		}
		end := start
		for end < len(data) && data[end] != '\n' {
			r.examined++
			end++
		}
		raw := data[start:end]
		if len(raw) > 0 && raw[len(raw)-1] == '\r' {
			raw = raw[:len(raw)-1]
		}
		n := 0
		for n < len(raw) && raw[len(raw)-1-n] == '\\' {
			n++
			r.examined++
		}
		kept := append(append([]byte(nil), raw[:len(raw)-n]...),
			makeSlash(n/2)...) // pairs survive; a lone trailing connector is dropped
		join := n%2 == 1

		if active {
			stripped, trimmed := skipSpaces(kept)
			if len(trimmed) == 0 && !join { // empty/white line terminates the chain
				finish()
				lines = append(lines, Line{Blank: true,
					Segments: []Segment{{PhysicalLine: phys}}})
			} else {
				if len(trimmed) == 0 { // "   \\" connects again but contributes nothing
					seg = append(seg, Segment{PhysicalLine: phys, Stripped: stripped,
						Start: len(body), Contributes: false})
				} else {
					seg = append(seg, Segment{PhysicalLine: phys, Stripped: stripped,
						Start: len(body), Contributes: len(trimmed) > 0})
					body = append(body, trimmed...)
				}
				if end == len(data) || !join {
					finish()
				}
			}
		} else {
			active, blank, comment = true, isBlank(raw), isComment(raw)
			seg = []Segment{{PhysicalLine: phys, Contributes: len(kept) > 0}}
			body = append(body, kept...)
			if end == len(data) || !join {
				finish()
			}
		}

		if end == len(data) {
			break
		}
		phys++
		start = end + 1
	}
	return lines
}

func isBlank(s []byte) bool {
	for _, b := range s {
		if b != ' ' && b != '\t' && b != '\f' {
			return false
		}
	}
	return true
}

func isComment(s []byte) bool {
	_, t := skipSpaces(s)
	return len(t) > 0 && (t[0] == '#' || t[0] == '!')
}

func makeSlash(n int) []byte {
	if n == 0 {
		return nil
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = '\\'
	}
	return b
}

func skipSpaces(s []byte) (int, []byte) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\f') {
		i++
	}
	return i, s[i:]
}
