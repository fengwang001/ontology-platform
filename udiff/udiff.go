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

// Patch is a parsed unified diff for one file pair.
type Patch struct {
	OldName string
	NewName string
	Hunks   []hunk.Hunk
}

// ErrMalformed is the sentinel for syntactically invalid patch text.
var ErrMalformed = errors.New("udiff: malformed patch")

type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("%s: line %d: %s", ErrMalformed, e.Line, e.Msg)
}
func (e *FormatError) Unwrap() error { return ErrMalformed }
func part(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
		return strconv.Itoa(start) + "," + strconv.Itoa(count)
	}
const markerNoNL = `\ No newline at end of file`

func (p Patch) Render() []byte {
	var b strings.Builder
	b.WriteString("--- " + p.OldName + "\n")
	b.WriteString("+++ " + p.NewName + "\n")
	for _, h := range p.Hunks {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", part(h.OldStart, h.OldCount), part(h.NewStart, h.NewCount))
		for _, r := range h.Rows {
			var l lines.Line
			switch r.Kind {
			case edit.Equal:
				b.WriteByte(' ')
				l = r.Old
			case edit.Delete:
				b.WriteByte('-')
				l = r.Old
			default:
				b.WriteByte('+')
				l = r.New
			}
			b.Write(l.Raw)
			if l.NoNewline {
				b.WriteString(markerNoNL + "\n")
			}
		}
	}
	return []byte(b.String())
}

type ParseOptions struct {
	MaxBytes int // <= 0: unlimited
	MaxHunks int // <= 0: unlimited
}

func Parse(data []byte, opt ParseOptions) (Patch, error) {
	if opt.MaxBytes > 0 && len(data) > opt.MaxBytes {
		return Patch{}, &FormatError{Line: 1, Msg: "patch exceeds byte limit"}
	}
	ls := lines.Split(data)
	if len(ls) < 2 ||
		!strings.HasPrefix(string(ls[0].Raw), "--- ") ||
		!strings.HasPrefix(string(ls[1].Raw), "+++ ") {
		return Patch{}, &FormatError{Line: 1, Msg: "missing file headers"}
	}
	p := Patch{trimLabel(ls[0].Raw[4:]), trimLabel(ls[1].Raw[4:]), nil}
	for i := 2; i < len(ls); {
		if !strings.HasPrefix(string(ls[i].Raw), "@@ ") {
			return Patch{}, &FormatError{Line: i + 1, Msg: "expected hunk header"}
		}
		if opt.MaxHunks > 0 && len(p.Hunks) >= opt.MaxHunks {
			return Patch{}, &FormatError{Line: i + 1, Msg: "hunk count exceeds limit"}
		}
		h, next, err := parseHunk(ls, i)
		if err != nil {
			return Patch{}, err
		}
		p.Hunks = append(p.Hunks, h)
		i = next
	}
	return p, nil
}

func parseHunk(ls []lines.Line, i int) (hunk.Hunk, int, error) {
	os, oc, ns, nc, ok := header(string(ls[i].Body()))
	if !ok {
		return hunk.Hunk{}, 0, &FormatError{Line: i + 1, Msg: "bad hunk header"}
	}
	h := hunk.Hunk{OldStart: os, OldCount: oc, NewStart: ns, NewCount: nc}
	goc, gnc := 0, 0
	j := i + 1
	for j < len(ls) && !strings.HasPrefix(string(ls[j].Raw), "@@ ") {
		raw := ls[j].Raw
		if len(raw) == 0 || raw[0] != ' ' && raw[0] != '-' && raw[0] != '+' {
			return hunk.Hunk{}, 0, &FormatError{Line: j + 1, Msg: "bad line prefix"}
		}
		body := append([]byte(nil), raw[1:]...)
		missing := false
		if j+1 < len(ls) && strings.HasPrefix(string(ls[j+1].Raw), markerNoNL) {
			missing, j = true, j+1
		}
		l := lines.Line{Raw: body, NoNewline: missing}
		switch raw[0] {
		case ' ':
			h.Rows = append(h.Rows, hunk.Row{Kind: edit.Equal, Old: l, New: l})
			goc, gnc = goc+1, gnc+1
		case '-':
			h.Rows = append(h.Rows, hunk.Row{Kind: edit.Delete, Old: l})
			goc++
		case '+':
			h.Rows = append(h.Rows, hunk.Row{Kind: edit.Insert, New: l})
			gnc++
		}
		j++
	}
	if goc != oc || gnc != nc {
		return hunk.Hunk{}, 0, &FormatError{Line: i + 1,
			Msg: fmt.Sprintf("header counts (%d,%d) mismatch body (%d,%d)", oc, nc, goc, gnc)}
	}
	return h, j, nil
}

func header(s string) (os, oc, ns, nc int, ok bool) {
	if !strings.HasPrefix(s, "@@ ") || !strings.HasSuffix(s, " @@") {
		return
	}
	s = s[3 : len(s)-3]
	parts := strings.SplitN(s, " ", 2)
	if len(parts) != 2 || parts[0][0] != '-' || parts[1][0] != '+' {
		return
	}
	readOne := func(t string) (int, int, bool) {
		t = t[1:]
		a, b, found := t, "1", false
		if i := strings.IndexByte(t, ','); i >= 0 {
			a, b, found = t[:i], t[i+1:], true
		}
		x, e1 := strconv.Atoi(a)
		y := 1
		var e2 error
		if found {
			y, e2 = strconv.Atoi(b)
		}
		return x, y, e1 == nil && e2 == nil
	}
	a, b, k1 := readOne(parts[0])
	c, d, k2 := readOne(parts[1])
	return a, b, c, d, k1 && k2
}
func trimLabel(raw []byte) string { return strings.TrimRight(string(raw), "\r\n") }
