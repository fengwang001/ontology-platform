package patch

import (
	"ontology/edit"
	"ontology/lines"
	"ontology/udiff"
)

// ApplyText parses diff and applies it to data. On any failure data is
// returned byte-for-byte unchanged. Parse failures carry Reason=FormatReason.
func ApplyText(data, diff []byte, opt Options, lim udiff.Limits) ([]byte, error) {
	p, err := udiff.Parse(diff, lim)
	if err != nil {
		return data, classifyParse(err)
	}
	return Apply(data, p, opt)
}

func classifyParse(err error) error {
	if err == udiff.ErrTooLarge {
		return &Error{Reason: FormatReason, Err: err}
	}
	return &Error{Reason: FormatReason, Hunk: 0, Err: err}
}

// Apply applies an already parsed patch atomically.
func Apply(data []byte, p *udiff.Patch, opt Options) ([]byte, error) {
	src := lines.Split(data)
	cur := make([]lines.Line, len(src))
	copy(cur, src)
	delta := 0
	for i := range p.Hunks {
		hh := p.Hunks[i]
		anchor := hh.OldStart + delta
		nxt, d, err := applyHunk(cur, hh, anchor, opt.Fuzz)
		if err != nil {
			if e, ok := err.(*Error); ok {
				e.Hunk = i + 1
			}
			return data, err
		}
		cur = nxt
		delta += d
	}
	return lines.Join(cur), nil
}

// Reverse returns a patch that undoes p (old/new sides and headers swapped).
func Reverse(p *udiff.Patch) *udiff.Patch {
	r := &udiff.Patch{OldName: p.NewName, NewName: p.OldName}
	for _, hh := range p.Hunks {
		nh := udiff.Hunk{
			OldStart: hh.NewStart, OldCount: hh.NewCount,
			NewStart: hh.OldStart, NewCount: hh.OldCount,
		}
		for _, row := range hh.Rows {
			op := row.Op
			if op == '-' {
				op = '+'
			} else if op == '+' {
				op = '-'
			}
			nr := row
			nr.Op = op
			nh.Rows = append(nh.Rows, nr)
		}
		r.Hunks = append(r.Hunks, nh)
	}
	return r
}

// Diff is a convenience helper: build a shortest script and its patch text.
func Diff(a, b []byte, ctx, maxD int, oldName, newName string) ([]byte, error) {
	la, lb := lines.Split(a), lines.Split(b)
	script, err := edit.Diff(la, lb, maxD, nil)
	if err != nil {
		return nil, err
	}
	p := udiff.Build(script, oldName, newName, ctx)
	return udiff.Render(p), nil
}
