// Package udiff renders and parses unified diff text.
package udiff

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

// NoNLText is the literal marker emitted after a final line without newline.
const NoNLText = `\ No newline at end of file`

// Hunk is one parsed/rendered hunk.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []Row
}

// Row is one body line. Mark means ' ', '-', or '+'.
type Row struct {
	Mark                          byte
	Old, New                      *lines.Line
	OldNL, NewNL                  bool // line had a terminator; meaningful on final lines
}

// Patch is a parsed unified diff.
type Patch struct {
	OldName string
	NewName string
	Hunks   []Hunk
}

// Limits caps parsing resources.
type Limits struct {
	MaxBytes int64 // 0 = unlimited
	MaxHunks int   // 0 = unlimited
}

var (
	// ErrFormat is any syntactic error in patch text.
	ErrFormat = errors.New("udiff: malformed patch")
)

// ParseError wraps ErrFormat with the 1-based line number in the patch text.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%v: line %d: %s", ErrFormat, e.Line, e.Msg)
}
func (e *ParseError) Unwrap() error { return ErrFormat }

// FromHunks builds a renderable patch from hunks using the given names.
func FromHunks(oldName, newName string, hs []hunk.Hunk) Patch {
	p := Patch{OldName: oldName, NewName: newName}
	for _, h := range hs {
		uh := Hunk{OldStart: h.OldStart, OldCount: h.OldCount,
			NewStart: h.NewStart, NewCount: h.NewCount}
		for i := range h.Steps {
			s := h.Steps[i]
			r := Row{Mark: " -+"[s.Op], Old: s.Old, New: s.New}
			if s.Old != nil {
				r.OldNL = lines.HasNL(*s.Old)
			}
			if s.New != nil {
				r.NewNL = lines.HasNL(*s.New)
			}
			uh.Rows = append(uh.Rows, r)
		}
	p.Hunks = append(p.Hunks, uh)
	}
	return p
}

// Render emits canonical unified diff text.
func (p Patch) Render() []byte {
	var b strings.Builder
	b.WriteString("--- " + p.OldName + "\n")
	b.WriteString("+++ " + p.NewName + "\n")
	for _, h := range p.Hunks {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", rangeText(h.OldStart, h.OldCount),
			rangeText(h.NewStart, h.NewCount))
		for _, r := range h.Rows {
			b.WriteByte(r.Mark)
			switch r.Mark {
			case ' ':
				b.Write(r.Old.Data)
			case '-':
				b.Write(r.Old.Data)
			case '+':
				b.Write(r.New.Data)
			}
			if (r.Mark == ' ' && !r.OldNL) || (r.Mark == '-' && !r.OldNL) ||
				(r.Mark == '+' && !r.NewNL) {
				b.WriteString(NoNLText + "\n")
			}
		}
	}
	return []byte(b.String())
}

func rangeText(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Reverse returns the inverse patch (b -> a).
func (p Patch) Reverse() Patch {
	q := Patch{OldName: p.NewName, NewName: p.OldName}
	for _, h := range p.Hunks {
		nh := Hunk{OldStart: h.NewStart, OldCount: h.NewCount,
			NewStart: h.OldStart, NewCount: h.OldCount}
		for i := len(h.Rows) - 1; i >= 0; i-- {
			r := h.Rows[i]
			switch r.Mark {
			case '-':
				r.Mark = '+'
			case '+':
				r.Mark = '-'
			}
			r.Old, r.New = r.New, r.Old
			r.OldNL, r.NewNL = r.NewNL, r.OldNL
			nh.Rows = append(nh.Rows, r)
		}
		q.Hunks = append(q.Hunks, nh)
	}
	return q
}

// Script converts parsed rows back to an ordered edit script.
func (h Hunk) Script() []edit.Step {
	var out []edit.Step
	for _, r := range h.Rows {
		s := edit.Step{}
		switch r.Mark {
		case ' ':
			s.Op, s.Old, s.New = edit.Equal, r.Old, r.New
		case '-':
			s.Op, s.Old = edit.Delete, r.Old
		case '+':
			s.Op, s.New = edit.Insert, r.New
		}
		out = append(out, s)
	}
	return out
}
