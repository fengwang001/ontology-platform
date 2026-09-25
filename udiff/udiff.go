// Package udiff renders and strictly parses unified-diff text.
package udiff

import (
	"errors"
	"fmt"
	"strconv"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

var (
	ErrFormat    = errors.New("udiff: format error")
	ErrTooLarge  = errors.New("udiff: patch exceeds resource limit")
	Marker       = []byte("\\ No newline at end of file")
)

// FormatError is ErrFormat with the 1-based text line number.
type FormatError struct{ Line int; What string }

func (e *FormatError) Error() string {
	return fmt.Sprintf("%s at line %d: %s", ErrFormat, e.Line, e.What)
}
func (e *FormatError) Unwrap() error { return ErrFormat }

// Row is one hunk body line.
type Row struct {
	Tag        byte
	Text, EOL  []byte
}

// Hunk is a parsed hunk header plus rows.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []Row
}

// Patch is a parsed or renderable unified diff.
type Patch struct {
	OldName, NewName string
	Hunks            []*Hunk
	Context          int
}

// Limits bounds parsing; zero means unlimited.
type Limits struct{ MaxBytes, MaxHunks int }

// Render builds unified-diff text with default "a"/"b" headers.
func Render(a, b []byte, ctx int) []byte { return RenderDiff(Diff(a, b, ctx)) }

// Diff computes hunks with deterministic tie-breaking (delete first).
func Diff(a, b []byte, ctx int) *Patch {
	la, lb := lines.Split(a), lines.Split(b)
	sc, _ := edit.Diff(la, lb, edit.Options{})
	p := &Patch{OldName: "a", NewName: "b", Context: ctx}
	for _, hh := range hunk.Build(sc.Ops, ctx) {
		h := &Hunk{OldStart: hh.OldStart, OldCount: hh.OldCount,
			NewStart: hh.NewStart, NewCount: hh.NewCount}
		for _, it := range hh.Items {
			h.Rows = append(h.Rows, Row{it.Tag, it.Text, it.EOL})
		}
		p.Hunks = append(p.Hunks, h)
	}
	return p
}

// RenderDiff serializes a Patch including no-newline markers.
func RenderDiff(p *Patch) []byte {
	out := []byte("--- " + p.OldName + "\n+++ " + p.NewName + "\n")
	for _, h := range p.Hunks {
		out = append(out, []byte("@@ -"+num(h.OldStart, h.OldCount)+
			" +"+num(h.NewStart, h.NewCount)+" @@\n")...)
		for _, r := range h.Rows {
			out = append(out, r.Tag)
			out = append(out, r.Text...)
			if len(r.EOL) > 0 {
				out = append(out, r.EOL...)
			} else {
				out = append(out, '\n')
				out = append(out, Marker...)
				out = append(out, '\n')
			}
		}
	}
	return out
}

func num(start, count int) string {
	s := strconv.Itoa(start)
	if count != 1 {
		s += "," + strconv.Itoa(count)
	}
	return s
}

// Parse strictly parses one unified diff, verifying declared counts.
func Parse(data []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, ErrTooLarge
	}
	pl := lines.Split(data)
	p := &Patch{}
	if len(pl) < 2 || len(pl[0].Text) < 4 || string(pl[0].Text[:4]) != "--- " {
		return nil, &FormatError{Line: 1, What: "missing --- header"}
	}
	if len(pl[1].Text) < 4 || string(pl[1].Text[:4]) != "+++ " {
		return nil, &FormatError{Line: 2, What: "missing +++ header"}
	}
	p.OldName = string(pl[0].Text[4:])
	p.NewName = string(pl[1].Text[4:])
	for i := 2; i < len(pl); {
		ln := i + 1
		h, n, err := parseHunk(pl, i, ln)
		if err != nil {
			return nil, err
		}
		if lim.MaxHunks > 0 && len(p.Hunks)+1 > lim.MaxHunks {
			return nil, ErrTooLarge
		}
		p.Hunks = append(p.Hunks, h)
		i = n
	}
	return p, nil
}

func parseHunk(pl []lines.Line, i, ln int) (*Hunk, int, error) {
	txt := string(pl[i].Text)
	h := &Hunk{}
	rem, err := eatAt(txt, "@@ -")
	if err != nil {
		return nil, i, &FormatError{Line: ln, What: "expected hunk header"}
	}
	h.OldStart, h.OldCount, rem, err = eatRange(rem)
	if err != nil {
		return nil, i, &FormatError{Line: ln, What: "bad old range"}
	}
	if len(rem) < 2 || rem[:2] != " +" {
		return nil, i, &FormatError{Line: ln, What: "expected ' +'"}
	}
	h.NewStart, h.NewCount, rem, err = eatRange(rem[2:])
	if err != nil {
		return nil, i, &FormatError{Line: ln, What: "bad new range"}
	}
	if rem != " @@" {
		return nil, i, &FormatError{Line: ln, What: "expected ' @@'"}
	}
	i++
	oc, nc := 0, 0
	for i < len(pl) {
		t := pl[i].Text
		s := string(t)
		if s == string(Marker) {
			if len(h.Rows) == 0 {
				return nil, i, &FormatError{Line: i + 1, What: "marker without row"}
			}
			h.Rows[len(h.Rows)-1].EOL = nil
			i++
			continue
		}
		if oc >= h.OldCount && nc >= h.NewCount {
			break
		}
		if len(s) == 0 {
			return nil, i, &FormatError{Line: i + 1, What: "row without prefix"}
		}
		tag := s[0]
		switch tag {
		case ' ', '-', '+':
		default:
			return nil, i, &FormatError{Line: i + 1, What: "bad row prefix"}
		}
		if tag == ' ' && len(t) < 1 {
			return nil, i, &FormatError{Line: i + 1, What: "bad context row"}
		}
		r := Row{Tag: tag, EOL: pl[i].EOL}
		if len(t) >= 1 {
			r.Text = t[1:]
		}
		h.Rows = append(h.Rows, r)
		if tag != '+' {
			oc++
		}
		if tag != '-' {
			nc++
		}
		i++
	}
	if oc != h.OldCount || nc != h.NewCount {
		return nil, i, &FormatError{Line: ln, What: "declared count mismatch"}
	}
	return h, i, nil
}

func eatAt(s, pre string) (string, error) {
	if len(s) < len(pre) || s[:len(pre)] != pre {
		return "", ErrFormat
	}
	return s[len(pre):], nil
}

func eatRange(s string) (start, count int, rest string, err error) {
	end := 0
	for end < len(s) && s[end] != ',' && s[end] != ' ' {
		end++
	}
	start, err = strconv.Atoi(s[:end])
	if err != nil {
		return 0, 0, s, err
	}
	count = 1
	if end < len(s) && s[end] == ',' {
		j := end + 1
		for j < len(s) && s[j] != ' ' {
			j++
		}
		count, err = strconv.Atoi(s[end+1 : j])
		if err != nil {
			return 0, 0, s, err
		}
	end = j
	}
	return start, count, s[end:], nil
}
