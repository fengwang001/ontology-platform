// Package logical joins physical lines of a .properties file into logical
// lines, mirroring java.util.Properties.LineReader.readLine: blank lines and
// comment lines are dropped, a trailing odd run of backslashes continues the
// line, and leading whitespace of a continued line is stripped. It is a
// single-pass state machine; every byte is examined at most once, which the
// inspected counter proves.
package logical

import (
	"bufio"
	"io"
)

// inspected counts how many bytes have been examined since the last reset.
var inspected int64

// Inspected reports the total number of byte inspections so far.
func Inspected() int64 { return inspected }

// ResetInspected zeroes the inspection counter.
func ResetInspected() { inspected = 0 }

// Line is one logical line: the joined text and the 1-based physical line
// number on which the logical line starts.
type Line struct {
	Text string
	Phys int
}

// Read consumes r and returns its logical lines in order.
func Read(r io.Reader) ([]Line, error) {
	br := bufio.NewReader(r)
	var lines []Line
	var buf []byte
	phys, start := 1, 1
	skipWS, comment, newLine := true, false, true
	appended, backslash, skipLF := false, false, false
	emit := func() {
		lines = append(lines, Line{Text: string(buf), Phys: start})
		buf = buf[:0]
		skipWS, comment, newLine = true, false, true
	}
	for {
		c, err := br.ReadByte()
		if err == io.EOF {
			if len(buf) > 0 {
				if backslash {
					buf = buf[:len(buf)-1]
				}
				emit()
			}
			return lines, nil
		}
		if err != nil {
			return nil, err
		}
		inspected++
		if skipLF {
			skipLF = false
			if c == '\n' {
				continue
			}
		}
		if skipWS {
			if c == ' ' || c == '\t' || c == '\f' {
				continue
			}
			if !appended && (c == '\r' || c == '\n') {
				phys++
				skipLF = c == '\r'
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
			if len(buf) == 0 {
				start = phys
			}
			buf = append(buf, c)
			if c == '\\' {
				backslash = !backslash
			} else {
				backslash = false
			}
			continue
		}
		// Reached end of a physical line.
		phys++
		skipLF = c == '\r'
		if comment || len(buf) == 0 {
			buf = buf[:0]
			skipWS, comment, newLine = true, false, true
			continue
		}
		if backslash {
			buf = buf[:len(buf)-1]
			skipWS, appended, backslash = true, true, false
			continue
		}
		emit()
	}
}
