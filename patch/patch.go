package patch

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"ontology/edit"
	"ontology/lines"
	"ontology/udiff"
)

var (
	ErrContext = errors.New("hunk context does not match")
	ErrOffset  = errors.New("hunk is outside fuzz offset")
)

type Options = udiff.Options
type ApplyError struct {
	Hunk int
	Err  error
}

func (e *ApplyError) Error() string { return fmt.Sprintf("hunk %d: %v", e.Hunk+1, e.Err) }
func (e *ApplyError) Unwrap() error { return e.Err }

func Apply(data, text []byte, o Options) ([]byte, error) {
	p, err := udiff.Parse(data, o)
	if err != nil {
		return nil, err
	}
	return ApplyPatch(p, text, o)
}

func ApplyPatch(p udiff.Patch, text []byte, o Options) ([]byte, error) {
	cur, fuzz, shift := lines.Split(text), 3, 0
	if o.Context > fuzz {
		fuzz = o.Context
	}
	for hi, h := range p.Hunks {
		pos, ok := find(cur, h.Ops, h.OldStart-1+shift, fuzz)
		if ok {
			cur, shift = replace(cur, h.Ops, pos), shift+h.NewCount-h.OldCount
			continue
		}
		cause := ErrContext
		if anywhere(cur, h.Ops) {
			cause = ErrOffset
		}
		return nil, &ApplyError{hi, cause}
	}
	return lines.Join(cur), nil
}

func Reverse(p udiff.Patch) udiff.Patch {
	for i, h := range p.Hunks {
		h.OldStart, h.NewStart = h.NewStart, h.OldStart
		h.OldCount, h.NewCount = h.NewCount, h.OldCount
		for j, op := range h.Ops {
			if op.Kind != edit.Equal {
				op.Kind = edit.Delete + edit.Insert - op.Kind
				op.Old, op.New = op.New, op.Old
				op.OldIndex, op.NewIndex = op.NewIndex, op.OldIndex
				h.Ops[j] = op
			}
		}
		p.Hunks[i] = h
	}
	p.OldName, p.NewName = p.NewName, p.OldName
	return p
}

func find(cur []lines.Line, ops []edit.Op, center, fuzz int) (int, bool) {
	pos, dist, found := 0, 0, false
	for i := 0; i <= len(cur); i++ {
		if !matches(cur, ops, i) {
			continue
		}
		d := abs(i - center)
		if !found || d < dist || d == dist && i < pos {
			pos, dist, found = i, d, true
		}
	}
	return pos, found && dist <= fuzz
}

func anywhere(cur []lines.Line, ops []edit.Op) bool {
	for i := 0; i <= len(cur); i++ {
		if matches(cur, ops, i) {
			return true
		}
	}
	return false
}

func matches(cur []lines.Line, ops []edit.Op, at int) bool {
	p := at
	for _, op := range ops {
		if op.Kind == edit.Insert {
			continue
		}
		if p >= len(cur) || !same(cur[p], op.Old) {
			return false
		}
		p++
	}
	return true
}

func replace(cur []lines.Line, ops []edit.Op, at int) []lines.Line {
	out := append([]lines.Line(nil), cur[:at]...)
	p := at
	for _, op := range ops {
		if op.Kind != edit.Delete {
			out = append(out, op.New)
		}
		if op.Kind != edit.Insert {
			p++
		}
	}
	return append(out, cur[p:]...)
}

func same(a, b lines.Line) bool {
	return bytes.Equal(a.Content, b.Content) && bytes.Equal(a.Newline, b.Newline)
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

type Document struct {
	mu      sync.RWMutex
	text    []byte
	version int64
	log     []udiff.Patch
}

func NewStore(initial []byte) *Document {
	return &Document{text: append([]byte(nil), initial...)}
}

func (d *Document) Apply(data []byte, o Options) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, perr := udiff.Parse(data, o)
	if perr != nil {
		return d.version, perr
	}
	next, err := ApplyPatch(p, d.text, o)
	if err != nil {
		return d.version, err
	}
	d.text, d.version = next, d.version+1
	d.log = append(d.log, p)
	return d.version, nil
}

func (d *Document) Snapshot() ([]byte, int64, []udiff.Patch) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]byte(nil), d.text...), d.version, append([]udiff.Patch(nil), d.log...)
}
