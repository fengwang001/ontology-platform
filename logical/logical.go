// Package logical assembles physical lines of a Java .properties stream
// into logical lines: comments, blank lines, and backslash continuations.
package logical

import "io"

func isWS(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

// Kind classifies a logical line.
type Kind int

const (
	Blank Kind = iota
	Comment
	Data
)

// Seg is one physical-line fragment that participates in a logical line.
type Seg struct {
	Text   string // fragment text, with leading whitespace stripped when continued
	Offset int    // byte offset of Text[0] within the whole input
}

// Line is one logical line assembled from one or more physical lines.
type Line struct {
	Kind Kind
	Segs []Seg
}

// Reader yields logical lines while counting bytes examined.
type Reader struct {
	data   []byte
	pos    int
	checks int64
}

// NewReader reads the whole properties stream.
func NewReader(r io.Reader) (*Reader, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return &Reader{data: data}, nil
}

// Checks reports the total number of bytes examined so far.
func (r *Reader) Checks() int64 { return r.checks }

// Location translates an absolute input byte offset to physical line/column (1-based).
func (r *Reader) Location(offset int) (int, int) {
	line, col := 1, 1
	for i := 0; i < offset && i < len(r.data); i++ {
		b := r.data[i]
		if b == '\n' || b == '\r' {
			line++
			col = 1
			if b == '\r' && i+1 < len(r.data) && r.data[i+1] == '\n' {
				i++
			}
			continue
		}
		col++
	}
	return line, col
}

// Next returns the next logical line, or nil at end of input.
func (r *Reader) Next() *Line {
	if r.pos >= len(r.data) {
		return nil
	}
	var segs []Seg
	continued := false
	for {
		if continued {
			// Strip leading whitespace of the continued physical line;
			// an empty / whitespace-only line terminates the logical line.
			for r.pos < len(r.data) {
				b := r.data[r.pos]
				r.checks++
				if b == '\n' || b == '\r' {
					r.consumeNL()
					goto done
				}
				if !isWS(b) {
					break
				}
				r.pos++
			}
			if r.pos >= len(r.data) {
				goto done
			}
		}
		segStart := r.pos
		run := 0
		for r.pos < len(r.data) {
			b := r.data[r.pos]
			r.checks++
			if b == '\n' || b == '\r' {
				break
			}
			if b == '\\' {
				run++
			} else {
				run = 0
			}
			r.pos++
		}
		if run%2 == 1 {
			// Odd trailing backslashes: the last one is the continuation
			// marker and is dropped; the newline is consumed.
			segs = append(segs, Seg{Text: string(r.data[segStart : r.pos-1]), Offset: segStart})
			r.consumeNL()
			continued = true
			continue
		}
		segs = append(segs, Seg{Text: string(r.data[segStart:r.pos]), Offset: segStart})
		r.consumeNL()
		break
	}
done:
	line := &Line{Segs: segs}
	line.Kind = r.classify(segs)
	return line
}

func (r *Reader) consumeNL() {
	if r.pos >= len(r.data) {
		return
	}
	if r.data[r.pos] == '\r' {
		r.pos++
		if r.pos < len(r.data) && r.data[r.pos] == '\n' {
			r.pos++
		}
		return
	}
	if r.data[r.pos] == '\n' {
		r.pos++
	}
}

func (r *Reader) classify(segs []Seg) Kind {
	first := true
	for _, s := range segs {
		i := 0
		if first {
			for i < len(s.Text) {
				b := s.Text[i]
				r.checks++
				if !isWS(b) {
					break
				}
				i++
			}
			first = false
		}
		if i < len(s.Text) {
			switch s.Text[i] {
			case '#', '!':
				return Comment
			default:
				return Data
			}
		}
	}
	return Blank
}
