// Package logical joins the physical lines of a Java .properties
// file into logical lines: comment and blank lines are dropped, a
// trailing odd run of backslashes continues a line onto the next
// physical line, and the leading whitespace of a continued line is
// stripped. It mirrors java.util.Properties' LineReader.
package logical

var checked int

// Checked reports how many input bytes have been inspected in total.
func Checked() int { return checked }

// Reset zeroes the inspection counter.
func Reset() { checked = 0 }

// Line is one logical line together with the physical origin of its
// bytes, used by the props package for error positions.
type Line struct {
	Text string
	Row  int // 1-based physical row of Text[0]
	rows []int32
	cols []int32
}

// Pos returns the 1-based physical row and column of Text[i].
func (l Line) Pos(i int) (row, col int) {
	if l.rows == nil {
		return l.Row, i + 1
	}
	return int(l.rows[i]), int(l.cols[i])
}

func isWS(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

// Lines splits data into logical lines. Each byte is inspected once;
// backslash parity is tracked incrementally, never rescanned.
func Lines(data []byte) []Line {
	var out []Line
	var buf []byte
	var rows, cols []int32
	start, row := 1, 1
	strip, odd, multi, fresh := true, false, false, true
	reset := func() {
		buf, rows, cols = buf[:0], rows[:0], cols[:0]
		strip, odd, multi, fresh = true, false, false, true
	}
	emit := func() {
		if len(buf) > 0 && buf[0] != '#' && buf[0] != '!' {
			ln := Line{Text: string(buf), Row: start}
			if multi {
				ln.rows = append([]int32(nil), rows...)
				ln.cols = append([]int32(nil), cols...)
			}
			out = append(out, ln)
		}
		reset()
	}
	for i := 0; i < len(data); row++ {
		if fresh {
			start, fresh = row, false
		}
		col := 0
		k := i
		for ; k < len(data); k++ {
			b := data[k]
			checked++
			if b == '\n' || b == '\r' {
				break
			}
			col++
			if strip && isWS(b) {
				continue
			}
			strip = false
			buf = append(buf, b)
			rows = append(rows, int32(row))
			cols = append(cols, int32(col))
			if b == '\\' {
				odd = !odd
			} else {
				odd = false
			}
		}
		i = k + 1
		if k < len(data) && data[k] == '\r' && i < len(data) && data[i] == '\n' {
			checked++
			i++
		}
		if odd { // continuation: drop the backslash, strip next line
			buf, rows, cols = buf[:len(buf)-1], rows[:len(rows)-1], cols[:len(cols)-1]
			odd, strip, multi = false, true, true
		} else {
			emit()
		}
	}
	if len(buf) > 0 { // EOF: a dangling backslash was already dropped
		emit()
	}
	return out
}
