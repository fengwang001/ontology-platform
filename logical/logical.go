// Package logical joins the physical lines of a Java .properties file
// into logical lines: comment and blank lines are dropped, a trailing
// odd run of backslashes continues the line, and leading whitespace of
// a continued line is stripped. It mirrors java.util.Properties'
// LineReader.readLine with a single pass over the input.
package logical

var inspected int64 // total examined bytes, for the complexity test

// Inspected reports how many input bytes have been examined so far.
func Inspected() int64 { return inspected }

// Join records where a continued physical line starts inside Text.
type Join struct {
	Pos  int // byte offset in Line.Text
	Phys int // 1-based physical line number
}

// Line is one logical line: non-blank, non-comment, continuations joined.
type Line struct {
	Text  string
	Phys  int    // 1-based physical line number of the first physical line
	Joins []Join // later physical lines merged by continuation
}

// Where maps a byte offset in Text to a 1-based physical line and column.
func (l Line) Where(off int) (line, col int) {
	line, col = l.Phys, off+1
	for _, j := range l.Joins {
		if j.Pos > off {
			break
		}
		line, col = j.Phys, off-j.Pos+1
	}
	return line, col
}

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

// Lines splits s into logical lines. Every input byte is examined once.
func Lines(s string) []Line {
	var out []Line
	var buf []byte
	var joins []Join
	phys, startPhys := 1, 1
	skipWS, comment, newLine, appended := true, false, true, false
	backslash, skipLF := false, false
	flush := func() {
		if !comment && len(buf) > 0 {
			out = append(out, Line{Text: string(buf), Phys: startPhys, Joins: joins})
		}
		buf, joins = nil, nil
		skipWS, comment, newLine, backslash = true, false, true, false
	}
	for i := 0; i < len(s); i++ {
		inspected++
		c := s[i]
		if skipLF { // swallow the '\n' of a "\r\n" pair
			skipLF = false
			if c == '\n' {
				continue
			}
		}
		if skipWS {
			if isWS(c) {
				continue
			}
			if !appended && (c == '\r' || c == '\n') { // blank physical line
				skipLF = c == '\r'
				phys++
				continue
			}
			skipWS, appended = false, false
		}
		if newLine {
			newLine = false
			if c == '#' || c == '!' {
				comment = true
				continue
			}
		}
		if c != '\n' && c != '\r' {
			buf = append(buf, c)
			if c == '\\' {
				backslash = !backslash
			} else {
				backslash = false
			}
			continue
		}
		skipLF = c == '\r'
		phys++
		switch {
		case comment || len(buf) == 0: // comment/blank: no continuation
			flush()
			startPhys = phys
		case backslash: // odd trailing run: join next physical line
			buf = buf[:len(buf)-1]
			backslash = false
			skipWS, appended = true, true
			joins = append(joins, Join{Pos: len(buf), Phys: phys})
		default:
			flush()
			startPhys = phys
		}
	}
	if backslash { // EOF right after an odd backslash: drop it
		buf = buf[:len(buf)-1]
	}
	flush()
	return out
}
