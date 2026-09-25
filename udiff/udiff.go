// Package udiff renders and parses unified diff text.
package udiff

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

// NoNL is the literal "no newline" marker line.
const NoNL = "\\ No newline at end of file"

// ErrMalified is the sentinel for parse failures.
var ErrMalformed = errors.New("udiff: malformed patch")

// ErrTooLarge reports a configured size/count limit was exceeded.
var ErrTooLarge = errors.New("udiff: patch exceeds size or hunk limit")

// Line is one rendered hunk row.
type Line struct {
	Kind edit.Kind
	Line lines.Line
	NoNL bool // this line has no terminator in its side
}

// Hunk mirrors hunk.Hunk with parsed metadata.
type Hunk struct {
	OldStart, OldN int
	NewStart, NewN int
	Lines          []Line
}

// Patch is a parsed unified diff.
type Patch struct {
	OldName, NewName string
	Hunks            []Hunk
}

// MalformedError locates a parse failure at a 1-based text line.
type MalformedError struct {
	TextLine int
	Reason   string
}

func (e *MalformedError) Error() string {
	return fmt.Sprintf("udiff: malformed patch at text line %d: %s", e.TextLine, e.Reason)
}
func (e *MalformedError) Unwrap() error { return ErrMalformed }

// Limits bounds patch parsing.
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// Build creates a Patch from two byte slices with C context lines.
func Build(a, b []byte, context int) *Patch {
	la, lb := lines.Split(a), lines.Split(b)
	s, err := edit.Diff(la, lb, -1)
	if err != nil {
		return nil
	}
	hs := hunk.Group(s, context)
	p := &Patch{OldName: "a", NewName: "b"}
	for _, hh := range hs {
		h := Hunk{OldStart: hh.OldStart, OldN: hh.OldN, NewStart: hh.NewStart, NewN: hh.NewN}
		for _, it := range hh.Items {
			h.Lines = append(h.Lines, Line{Kind: it.Kind, Line: it.Line})
		}
		p.Hunks = append(p.Hunks, h)
	}
	return p
}

// Render prints a patch in unified format.
func Render(p *Patch) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--- %s\n", p.OldName)
	fmt.Fprintf(&buf, "+++ %s\n", p.NewName)
	for _, h := range p.Hunks {
		fmt.Fprintf(&buf, "@@ -%s +%s @@\n", rng(h.OldStart, h.OldN), rng(h.NewStart, h.NewN))
		for i, ln := range h.Lines {
			prefix := byte(' ')
			switch ln.Kind {
			case edit.Delete:
				prefix = '-'
			case edit.Insert:
				prefix = '+'
			}
			buf.WriteByte(prefix)
			buf.Write(ln.Line.Content)
			if ln.Line.NL() {
				buf.WriteString(ln.Line.End)
				continue
			}
			buf.WriteByte('\n')
			buf.WriteString(NoNL)
			buf.WriteByte('\n')
			_ = i
		}
	}
	return buf.Bytes()
}

func rng(start, n int) string {
	if n == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, n)
}

// Diff is shorthand for Render(Build(a,b,context)).
func Diff(a, b []byte, context int) []byte { return Render(Build(a, b, context)) }
