// Package udiff renders and parses unified diff text, preserving terminators.
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

type Row struct {
	Kind             edit.Kind
	Text             string
	OldEOL, NewEOL   []byte
	OldNoNL, NewNoNL bool
}
type Hunk struct {
	S0, N0, S1, N1 int
	Rows           []Row
}
type Patch struct {
	OldLabel, NewLabel string
	Hunks              []*Hunk
}

// FormatError is malformed input; Line is 1-based. Match with errors.Is(ErrFormat).
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string { return fmt.Sprintf("udiff: line %d: %s", e.Line, e.Msg) }
func (e *FormatError) Unwrap() error { return ErrFormat }
func bad(n int, m string) error      { return &FormatError{n, m} }

var (
	ErrFormat = errors.New("udiff: format error")
	nonl      = `\ No newline at end of file`
)

// Build diffs a and b with C context lines.
func Build(a, b []byte, C int) *Patch {
	var df edit.Differ
	ops, err := df.Diff(lines.Split(a), lines.Split(b), edit.Options{})
	if err != nil {
		panic(err)
	}
	p := &Patch{OldLabel: "old", NewLabel: "new"}
	for _, h := range hunk.Build(ops, C) {
		nh := &Hunk{h.OldStart, h.OldCount, h.NewStart, h.NewCount, nil}
		for _, l := range h.Lines {
			r := Row{Kind: l.K, OldNoNL: l.OldNoNL, NewNoNL: l.NewNoNL}
			if l.K != edit.Insert {
				r.Text, r.OldEOL = string(l.Op.A.Text), l.Op.A.EOL
			}
			if l.K != edit.Delete {
				if l.K == edit.Insert {
					r.Text = string(l.Op.B.Text)
				}
				r.NewEOL = l.Op.B.EOL
			}
			nh.Rows = append(nh.Rows, r)
		}
		p.Hunks = append(p.Hunks, nh)
	}
	return p
}

func rng(s, n int) string {
	if n == 1 {
		return strconv.Itoa(s)
	}
	return strconv.Itoa(s) + "," + strconv.Itoa(n)
}

func putRow(b *strings.Builder, pfx byte, r *Row) {
	b.WriteByte(pfx)
	b.WriteString(r.Text)
	no, eol := r.OldNoNL, r.OldEOL
	if pfx == '+' {
		no, eol = r.NewNoNL, r.NewEOL
	}
	if no {
		if pfx == ' ' {
			b.WriteByte('\n')
		}
		b.WriteString(nonl + "\n")
		return
	}
	b.Write(eol)
}

// Render returns canonical diff bytes (nil when empty).
func (p *Patch) Render() []byte {
	if len(p.Hunks) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", p.OldLabel, p.NewLabel)
	for _, h := range p.Hunks {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", rng(h.S0, h.N0), rng(h.S1, h.N1))
		for i := range h.Rows {
			r := &h.Rows[i]
			switch r.Kind {
			case edit.Delete:
				putRow(&b, '-', r)
			case edit.Insert:
				putRow(&b, '+', r)
			default:
				putRow(&b, ' ', r)
			}
		}
	}
	return []byte(b.String())
}
