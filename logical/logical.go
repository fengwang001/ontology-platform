// Package logical joins physical lines of a Java .properties file into
// logical lines, dropping comment and blank lines. It mirrors the line
// handling of java.util.Properties.LineReader.
package logical

import "io"

// inspected counts byte examinations to prove the scan is linear.
var inspected int64

// Inspected reports how many input bytes have been examined since the
// last ResetInspected call.
func Inspected() int64 { return inspected }

// ResetInspected zeroes the inspection counter.
func ResetInspected() { inspected = 0 }

// Line is a logical line plus the 1-based physical line it starts on.
type Line struct {
	Text string
	Phys int
}

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

// Lines reads r fully and returns its logical lines. Lines whose first
// non-blank character is '#' or '!' are comments and are dropped, as are
// blank lines. A trailing odd run of backslashes continues a line: the
// last backslash is removed and the next physical line is appended after
// stripping its leading blanks. A lone trailing backslash at EOF is
// dropped. Every input byte is examined at most twice.
func Lines(r io.Reader) ([]Line, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []Line
	var buf []byte
	n := len(data)
	i, phys := 0, 1
	for i < n {
		start := phys
		buf = buf[:0]
		skipWS, comment, first := true, false, true
		done := false
		for !done {
			run := 0 // trailing backslash run of the current segment
			for i < n {
				c := data[i]
				inspected++
				if c == '\n' || c == '\r' {
					break
				}
				if skipWS {
					if isWS(c) {
						i++
						continue
					}
					skipWS = false
					if first && (c == '#' || c == '!') {
						comment = true
					}
				}
				if !comment {
					buf = append(buf, c)
					if c == '\\' {
						run++
					} else {
						run = 0
					}
				}
				i++
			}
			if i < n { // consume the line terminator
				if data[i] == '\r' && i+1 < n && data[i+1] == '\n' {
					inspected++
					i += 2
				} else {
					i++
				}
				phys++
			}
			first = false
			switch {
			case comment || run%2 == 0:
				done = true
			default: // continuation: drop the backslash
				buf = buf[:len(buf)-1]
				if done = i >= n; !done {
					skipWS = true
				}
			}
		}
		if !comment && len(buf) > 0 {
			out = append(out, Line{Text: string(buf), Phys: start})
		}
	}
	return out, nil
}
