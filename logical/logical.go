// Package logical turns the physical lines of a .properties stream into
// logical lines, applying comment/blank-line recognition and backslash
// line continuation. It depends on no other package.
package logical

import (
	"bufio"
	"io"
)

// Seg is one physical-line fragment that participates in a logical line.
// Line is the 1-based physical line number; Col is the 1-based byte column
// of the fragment's first character (after continuation leading-space skip).
type Seg struct {
	Text []byte
	Line int // 1-based physical line number of this fragment
	Col  int // 1-based byte column of Text[0] after leading-space stripping
}

// Line is one assembled logical line.
type Line struct {
	Segs      []Seg
	IsComment bool
}

// Reader reads logical lines.
type Reader struct {
	br     *bufio.Reader
	phys   int   // physical lines consumed so far
	segs   []Seg // accumulator for the logical line in progress
	checks int64 // unexported counter of byte inspections
}

// NewReader constructs a Reader.
func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReader(r)}
}

// Checks reports the total number of bytes whose content was inspected.
func (r *Reader) Checks() int64 { return r.checks }

func isWS(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

// readPhys reads one physical line without its line terminator. ok is false
// only when no bytes remained before EOF.
func (r *Reader) readPhys() (data []byte, line int, ok bool, err error) {
	data, err := r.br.ReadBytes('\n')
	if len(data) == 0 {
		return nil, r.phys, false, err
	}
	r.phys++
	n := len(data)
	if n > 0 && data[n-1] == '\n' {
		data = data[:n-1]
		n--
		if n > 0 && data[n-1] == '\r' {
			data = data[:n-1]
		}
	}
	return data, r.phys, true, err
}

// nextSeg reads the next physical line, strips its leading whitespace when
// skipLead is true, and returns the fragment plus the trailing-backslash
// parity information: cont=true means an odd run of backslashes ends it.
// noData is true when EOF was reached with zero bytes read.
func (r *Reader) nextSeg(skipLead bool) (s Seg, cont, noData bool, err error) {
	data, line, ok, rerr := r.readPhys()
	if !ok {
		return Seg{}, false, true, rerr
	}
	s.Line = line
	off := 0
	if skipLead {
		for off < len(data) && isWS(data[off]) {
			off++
		}
		r.checks += int64(off) // leading whitespace is examined
	}
	s.Col = off + 1
	s.Text = data[off:]

	run := 0
	for run < len(s.Text) && s.Text[len(s.Text)-1-run] == '\\' {
		run++
	}
	r.checks += int64(run) // backslash parity is probed backward from the end
	cont = run%2 == 1
	if cont {
		s.Text = s.Text[:len(s.Text)-1] // the joining backslash is consumed
	}
	return s, cont, false, rerr
}

// Next returns the next non-blank logical line, or io.EOF at end of input.
func (r *Reader) Next() (*Line, error) {
	for {
		r.segs = r.segs[:0]

		s, cont, noData, err := r.nextSeg(false)
		if noData {
			return nil, io.EOF
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		r.segs = append(r.segs, s)

		for cont {
			c, c2, none, e := r.nextSeg(true)
			if none {
				break // dangling backslash at EOF: it was already dropped
			}
			if e != nil && e != io.EOF {
				return nil, e
			}
			r.segs = append(r.segs, c)
			cont = false
			if e == io.EOF {
				break
			}
			cont = c2
		}

		out := &Line{Segs: append([]Seg(nil), r.segs...)}
		head := out.Segs[0].Text
		i := 0
		for i < len(head) && isWS(head[i]) {
			i++
		}
		if i == len(head) {
			if err == io.EOF {
				return nil, io.EOF
			}
			continue // blank logical line
		}
		out.IsComment = head[i] == '#' || head[i] == '!'
		if out.IsComment {
			continue // comments carry no data
		}
		return out, nil
	}
}
