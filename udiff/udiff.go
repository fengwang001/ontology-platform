// Package udiff renders and parses unified-diff text.
package udiff

import (
	"errors"
	"fmt"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

// Hunk is one parsed hunk.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []hunk.Row
}

// Patch is a parsed unified diff for one file pair.
type Patch struct {
	OldName, NewName string
	Hunks            []Hunk
}

// Limits caps accepted patches. Zero means unlimited.
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// FormatError is a malformed-patch error carrying the 1-based text line.
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("udiff: format error at patch line %d: %s", e.Line, e.Msg)
}

// ErrTooLarge reports a resource-limit violation.
var ErrTooLarge = errors.New("udiff: patch exceeds configured limit")

// Build creates a patch from a script. Empty scripts yield an empty patch.
func Build(script []edit.Step, oldName, newName string, ctx int) *Patch {
	hs := hunk.Build(script, ctx)
	p := &Patch{OldName: oldName, NewName: newName}
	for _, hh := range hs {
		p.Hunks = append(p.Hunks, Hunk{
			OldStart: hh.OldStart, OldCount: hh.OldCount,
			NewStart: hh.NewStart, NewCount: hh.NewCount, Rows: hh.Rows,
		})
	}
	return p
}

// Render writes unified-diff bytes.
func Render(p *Patch) []byte {
	var b []byte
	if p == nil || len(p.Hunks) == 0 {
		return b
	}
	b = append(b, "--- "+p.OldName+"\n"...)
	b = append(b, "+++ "+p.NewName+"\n"...)
	for _, hh := range p.Hunks {
		b = append(b, hh.head()...)
		for _, r := range hh.Rows {
			b = append(b, r.Op)
			b = append(b, r.Text...)
			b = term(r)
			if r.NoNL {
				b = append(b, "\\ No newline at end of file\n"...)
			}
		}
	}
	return b
}

func (h Hunk) head() []byte {
	return []byte(fmt.Sprintf("@@ -%s +%s @@\n", spec(h.OldStart, h.OldCount), spec(h.NewStart, h.NewCount)))
}

func spec(start, count int) string {
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

func term(r hunk.Row) []byte {
	if !r.HasNL {
		return nil
	}
	if r.CRLF {
		return []byte("\r\n")
	}
	return []byte("\n")
}

// Parse strictly parses diff text under the given limits (zero value = none).
func Parse(data []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, ErrTooLarge
	}
	ls := lines.Split(data)
	p := &Patch{}
	if len(ls) == 0 {
		return p, nil
	}
	lineNo := 0
	take := func() (lines.Line, bool) {
		if lineNo >= len(ls) {
			return nil, false
		}
		l := ls[lineNo]
		lineNo++
		return l, true
	}
	raw, ok := take()
	if !ok || !pref(raw, "--- ") {
		return nil, &FormatError{Line: lineNo, Msg: "missing '--- ' header"}
	}
	p.OldName = string(raw.Content()[4:])
	raw, ok = take()
	if !ok || !pref(raw, "+++ ") {
		return nil, &FormatError{Line: lineNo, Msg: "missing '+++ ' header"}
	}
	p.NewName = string(raw.Content()[4:])
	for lineNo < len(ls) {
		raw, _ = take()
		if !pref(raw, "@@") {
			return nil, &FormatError{Line: lineNo, Msg: "expected '@@' hunk header"}
		}
		h, err := parseHead(string(raw.Content()), lineNo)
		if err != nil {
			return nil, err
		}
		if lim.MaxHunks > 0 && len(p.Hunks)+1 > lim.MaxHunks {
			return nil, ErrTooLarge
		}
		p.Hunks = append(p.Hunks, *h)
		cur := &p.Hunks[len(p.Hunks)-1]
		remO, remN := h.OldCount, h.NewCount
		for remO > 0 || remN > 0 {
			raw, ok = take()
			if !ok {
				return nil, &FormatError{Line: lineNo, Msg: "hunk truncated: fewer body lines than declared"}
			}
			if pref(raw, "\\") {
				if len(cur.Rows) == 0 {
					return nil, &FormatError{Line: lineNo, Msg: "no-newline marker without preceding row"}
				}
				cur.Rows[len(cur.Rows)-1].NoNL = true
				continue
			}
			if err := parseBody(cur, raw, lineNo, &remO, &remN); err != nil {
				return nil, err
			}
		}
		if lineNo < len(ls) && pref(ls[lineNo], "\\") {
			raw, _ = take()
			cur.Rows[len(cur.Rows)-1].NoNL = true
		}
	}
	return p, nil
}

func pref(l lines.Line, s string) bool {
	return len(l) >= len(s) && string(l[:len(s)]) == s
}

func parseHead(s string, no int) (*Hunk, error) {
	if len(s) < 4 || s[:3] != "@@ " {
		return nil, &FormatError{Line: no, Msg: "invalid hunk header"}
	}
	pos := 3
	var starts, counts [2]int
	for side := 0; side < 2; side++ {
		want := byte('-')
		if side == 1 {
			want = '+'
		}
		if pos >= len(s) || s[pos] != want {
			return nil, &FormatError{Line: no, Msg: "invalid hunk header range"}
		}
		pos++
		v, n, ok := readInt(s[pos:])
		if !ok {
			return nil, &FormatError{Line: no, Msg: "invalid hunk start"}
		}
		pos += n
		cnt := 1
		if pos < len(s) && s[pos] == ',' {
			c, n2, ok2 := readInt(s[pos+1:])
			if !ok2 {
				return nil, &FormatError{Line: no, Msg: "invalid hunk count"}
			}
			cnt = c
			pos += 1 + n2
		}
		starts[side], counts[side] = v, cnt
		if side == 0 {
			if pos >= len(s) || s[pos] != ' ' {
				return nil, &FormatError{Line: no, Msg: "missing ' +' in hunk header"}
			}
			pos++
		}
	}
	if pos+3 > len(s) || s[pos] != ' ' || s[pos+1] != '@' || s[pos+2] != '@' {
		return nil, &FormatError{Line: no, Msg: "hunk header must end with @@"}
	}
	return &Hunk{OldStart: starts[0], OldCount: counts[0], NewStart: starts[1], NewCount: counts[1]}, nil
}

func readInt(s string) (int, int, bool) {
	if len(s) == 0 || s[0] < '0' || s[0] > '9' {
		return 0, 0, false
	}
	v, n := 0, 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		v = v*10 + int(s[n]-'0')
		n++
	}
	return v, n, true
}

func parseBody(h *Hunk, raw lines.Line, no int, remO, remN *int) error {
	if len(raw) == 0 || raw[0] != ' ' && raw[0] != '-' && raw[0] != '+' {
		return &FormatError{Line: no, Msg: "body line must start with space, '-' or '+'"}
	}
	op, body := raw[0], raw[1:]
	switch op {
	case ' ':
		if *remO <= 0 || *remN <= 0 {
			return &FormatError{Line: no, Msg: "more body lines than header declares"}
		}
		*remO, *remN = *remO-1, *remN-1
	case '-':
		if *remO <= 0 {
			return &FormatError{Line: no, Msg: "more old-side lines than header declares"}
		}
		*remO--
	case '+':
		if *remN <= 0 {
			return &FormatError{Line: no, Msg: "more new-side lines than header declares"}
		}
		*remN--
	}
	r := hunk.Row{Op: op, HasNL: raw.HasNL()}
	r.Text = body.Content()
	if body.HasNL() && len(body) >= 2 && body[len(body)-2] == '\r' {
		r.CRLF = true
	}
	h.Rows = append(h.Rows, r)
	return nil
}
