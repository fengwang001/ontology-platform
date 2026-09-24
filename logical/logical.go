package logical

import "io"

func isWS(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

// Segment records where a physical line contributes to a logical line.
type Segment struct {
	Line int // physical line number, 1-based
	Col  int // 1-based column of the first kept byte on that physical line
	Off  int // offset of that byte in Line.Text
}

// Line is one logical line assembled from physical lines.
type Line struct {
	Text string
	Segs []Segment
}

// Scanner joins physical lines into logical lines.
type Scanner struct {
	examined int64
}

// Examined returns the total number of bytes inspected so far.
func (s *Scanner) Examined() int64 { return s.examined }

// ReadAll reads all logical lines from r.
func (s *Scanner) ReadAll(r io.Reader) ([]Line, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var lines []Line
	var buf []byte
	var segs []Segment
	cont := false // previous physical line ended a continuation
	phys := 0
	for i := 0; i <= len(data); {
		if i == len(data) && len(buf) == 0 && !cont {
			break
		}
		// Slice out one physical line; terminators are \n, \r, \r\n.
		j := i
		for j < len(data) && data[j] != '\n' && data[j] != '\r' {
			j++
		}
		content := data[i:j]
		next := j
		if j < len(data) {
			next = j + 1
			if data[j] == '\r' && next < len(data) && data[next] == '\n' {
				next++
			}
		}
		s.examined += int64(next - i)
		phys++
		i = next

		k := 0
		for k < len(content) && isWS(content[k]) {
			k++
		}
		rest := content[k:]
		if !cont {
			if len(rest) == 0 || rest[0] == '#' || rest[0] == '!' {
				continue // blank or comment line
			}
			segs = nil
		}
		if len(rest) > 0 {
			segs = append(segs, Segment{Line: phys, Col: k + 1, Off: len(buf)})
			buf = append(buf, rest...)
		}
		// A continuation needs an odd run of trailing backslashes; the run
		// is scanned backwards from the line end, never from the start.
		n := 0
		for n < len(rest) && rest[len(rest)-1-n] == '\\' {
			n++
		}
		s.examined += int64(n)
		if len(rest) > 0 && n%2 == 1 {
			buf = buf[:len(buf)-1] // drop the continuation backslash
			cont = true
			continue
		}
		// Emit: even backslashes, or a blank/empty continuation segment
		// (rest == "" terminates the logical line), or EOF.
		lines = append(lines, Line{Text: string(buf), Segs: segs})
		buf = buf[:0]
		cont = false
	}
	return lines, nil
}
