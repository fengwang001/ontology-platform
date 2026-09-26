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

type Options struct{ Context, MaxD, MaxBytes, MaxHunks int }
type Patch struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}
type ParseError struct {
	Line int
	Err  error
}

var (
	ErrFormat   = errors.New("malformed patch")
	ErrTooLarge = errors.New("patch exceeds resource limit")
	marker      = "\\ No newline at end of file"
)

func (e *ParseError) Error() string { return fmt.Sprintf("line %d: %v", e.Line, e.Err) }
func (e *ParseError) Unwrap() error { return e.Err }

func Diff(a, b []byte, o Options) (Patch, error) {
	if o.Context < 0 {
		o.Context = 3
	}
	en := edit.Engine{MaxD: o.MaxD}
	ops, err := en.Diff(lines.Split(a), lines.Split(b))
	if err != nil {
		return Patch{}, err
	}
	return Patch{"a", "b", hunk.Groups(ops, o.Context)}, nil
}

func Render(p Patch) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", p.OldName, p.NewName)
	for _, h := range p.Hunks {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", side(h.OldStart, h.OldCount), side(h.NewStart, h.NewCount))
		for _, op := range h.Ops {
			if op.Kind == edit.Equal {
				body(&b, ' ', op.Old)
			}
			if op.Kind == edit.Delete {
				body(&b, '-', op.Old)
			}
			if op.Kind == edit.Insert {
				body(&b, '+', op.New)
			}
		}
	}
	return []byte(b.String())
}

func side(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

func body(b *strings.Builder, p byte, l lines.Line) {
	b.WriteByte(p)
	b.Write(l.Content)
	b.Write(l.Newline)
	if len(l.Newline) == 0 {
		b.WriteString(marker + "\n")
	}
}

func Parse(data []byte, o Options) (Patch, error) {
	if o.MaxBytes > 0 && len(data) > o.MaxBytes {
		return Patch{}, ErrTooLarge
	}
	r := lines.Split(data)
	if len(r) < 2 || raw(r[0])[:4] != "--- " || raw(r[1])[:4] != "+++ " {
		return Patch{}, &ParseError{1, ErrFormat}
	}
	p := Patch{string(r[0].Content[4:]), string(r[1].Content[4:]), nil}
	for i := 2; i < len(r); {
		if o.MaxHunks > 0 && len(p.Hunks) >= o.MaxHunks {
			return Patch{}, ErrTooLarge
		}
		h, next, err := parseHunk(r, i)
		if err != nil {
			return Patch{}, err
		}
		p.Hunks, i = append(p.Hunks, h), next
	}
	return p, nil
}

func raw(l lines.Line) string { return string(l.Content) + string(l.Newline) }

func parseHunk(r []lines.Line, at int) (hunk.Hunk, int, error) {
	f := strings.Fields(raw(r[at]))
	h := hunk.Hunk{}
	if len(f) < 3 || f[0] != "@@" {
		return h, 0, &ParseError{at + 1, ErrFormat}
	}
	var err error
	if h.OldStart, h.OldCount, err = rng(f[1], '-'); err != nil {
		return h, 0, &ParseError{at + 1, err}
	}
	if h.NewStart, h.NewCount, err = rng(f[2], '+'); err != nil {
		return h, 0, &ParseError{at + 1, err}
	}
	old, new, i := 0, 0, at+1
	for old < h.OldCount || new < h.NewCount {
		if i >= len(r) {
			return h, 0, &ParseError{len(r), ErrFormat}
		}
		s := raw(r[i])
		if len(s) == 0 || s[0] != ' ' && s[0] != '-' && s[0] != '+' {
			return h, 0, &ParseError{i + 1, ErrFormat}
		}
		op := parseOp(s[0], r[i])
		if i+1 < len(r) && string(r[i+1].Content) == marker {
			stripNL(&op)
			i++
		}
		h.Ops = append(h.Ops, op)
		if op.Kind != edit.Insert {
			old++
		}
		if op.Kind != edit.Delete {
			new++
		}
		i++
	}
	return h, i, nil
}

func rng(v string, sign byte) (int, int, error) {
	if len(v) < 2 || v[0] != sign {
		return 0, 0, ErrFormat
	}
	startText, countText := v[1:], "1"
	if i := strings.IndexByte(startText, ','); i >= 0 {
		startText, countText = startText[:i], startText[i+1:]
	}
	start, e1 := strconv.Atoi(startText)
	count, e2 := strconv.Atoi(countText)
	if e1 != nil || e2 != nil || start < 0 || count < 0 {
		return 0, 0, ErrFormat
	}
	return start, count, nil
}

func parseOp(k byte, r lines.Line) edit.Op {
	l := lines.Line{append([]byte(nil), r.Content[1:]...), append([]byte(nil), r.Newline...)}
	if k == ' ' {
		return edit.Op{Kind: edit.Equal, Old: l, New: l}
	}
	if k == '-' {
		return edit.Op{Kind: edit.Delete, Old: l}
	}
	return edit.Op{Kind: edit.Insert, New: l}
}

func stripNL(op *edit.Op) {
	if op.Kind == edit.Equal {
		op.Old.Newline, op.New.Newline = nil, nil
	} else if op.Kind == edit.Delete {
		op.Old.Newline = nil
	} else {
		op.New.Newline = nil
	}
}
