package udiff

import (
	"bytes"
	"fmt"
	"strconv"

	"ontology/hunk"
	"ontology/lines"
)

type Row struct {
	Kind             byte
	Line             lines.Line
	NoNLOld, NoNLNew bool
}
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []Row
}
type Patch struct{ Hunks []Hunk }
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("malformed patch at line %d: %s", e.Line, e.Msg)
}

func Build(hs []hunk.Hunk, a, b []lines.Line) *Patch {
	p := &Patch{}
	for _, h := range hs {
		uh := Hunk{h.OldStart, h.OldCount, h.NewStart, h.NewCount, nil}
		for _, r := range h.Rows {
			l := a[r.Old]
			if r.Kind == '+' {
				l = b[r.New]
			}
			row := Row{Kind: r.Kind, Line: l}
			row.NoNLOld = r.Kind != '+' && r.Old == len(a)-1 && len(a) > 0 && a[r.Old].NL == ""
			row.NoNLNew = r.Kind != '-' && r.New == len(b)-1 && len(b) > 0 && b[r.New].NL == ""
			uh.Rows = append(uh.Rows, row)
		}
		p.Hunks = append(p.Hunks, uh)
	}
	return p
}

func (p *Patch) Render() []byte {
	var buf bytes.Buffer
	buf.WriteString("--- a\n+++ b\n")
	for _, h := range p.Hunks {
		fmt.Fprintf(&buf, "@@ -%s +%s @@\n", hdr(h.OldStart, h.OldCount), hdr(h.NewStart, h.NewCount))
		for i := range h.Rows {
			r := &h.Rows[i]
			buf.WriteByte(r.Kind)
			buf.Write(r.Line.Content)
			buf.WriteString(r.Line.NL)
			if r.NoNLOld || r.NoNLNew {
				buf.WriteString("\\ No newline at end of file\n")
			}
		}
	}
	return buf.Bytes()
}

func hdr(s, c int) string {
	if c == 1 {
		return strconv.Itoa(s)
	}
	return fmt.Sprintf("%d,%d", s, c)
}

var marker = []byte("\\ No newline at end of file")

func Parse(text []byte, maxHunks int) (*Patch, error) {
	ls := lines.Split(text)
	if len(ls) < 2 || string(ls[0].Content) != "--- a" || string(ls[1].Content) != "+++ b" {
		return nil, &FormatError{1, "missing ---/+++ headers"}
	}
	p := &Patch{}
	i := 2
	for i < len(ls) {
		if !bytes.HasPrefix(ls[i].Content, []byte("@@ ")) {
			return nil, &FormatError{i + 1, "expected hunk header"}
		}
		if maxHunks > 0 && len(p.Hunks) >= maxHunks {
			return nil, &FormatError{i + 1, "too many hunks"}
		}
		os, oc, ns, nc, err := parseHdr(ls[i].Content, i+1)
		if err != nil {
			return nil, err
		}
		h := Hunk{OldStart: os, OldCount: oc, NewStart: ns, NewCount: nc}
		i++
		goc, gnc := 0, 0
		for goc < oc || gnc < nc {
			if i >= len(ls) {
				return nil, &FormatError{len(ls), "hunk truncated"}
			}
			c := ls[i].Content
			if len(c) == 0 || (c[0] != ' ' && c[0] != '-' && c[0] != '+') {
				return nil, &FormatError{i + 1, "bad hunk line prefix"}
			}
			nl := ls[i].NL
			i++
			if i < len(ls) && bytes.Equal(ls[i].Content, marker) {
				nl, i = "", i+1
			}
			k := c[0]
			goc += b2i(k != '+')
			gnc += b2i(k != '-')
			h.Rows = append(h.Rows, Row{k, lines.Line{Content: append([]byte(nil), c[1:]...), NL: nl}, false, false})
		}
		p.Hunks = append(p.Hunks, h)
	}
	return p, nil
}

func parseHdr(c []byte, n int) (int, int, int, int, error) {
	r := c[3:]
	e := bytes.Index(r, []byte(" @@"))
	if e < 0 || r[0] != '-' || !bytes.Contains(r[:e], []byte(" +")) {
		return 0, 0, 0, 0, &FormatError{n, "bad hunk header"}
	}
	i := bytes.Index(r[:e], []byte(" +"))
	s1, c1, e1 := one(r[1:i], n)
	s2, c2, e2 := one(r[i+2:e], n)
	if e1 != nil || e2 != nil {
		return 0, 0, 0, 0, &FormatError{n, "bad hunk range"}
	}
	return s1, c1, s2, c2, nil
}

func one(f []byte, n int) (int, int, error) {
	xs := bytes.SplitN(f, []byte(","), 2)
	s, err := strconv.Atoi(string(xs[0]))
	if err != nil || s < 0 {
		return 0, 0, &FormatError{n, "bad hunk range"}
	}
	if len(xs) == 1 {
		return s, 1, nil
	}
	c, err := strconv.Atoi(string(xs[1]))
	if err != nil || c < 0 {
		return 0, 0, &FormatError{n, "bad hunk range"}
	}
	return s, c, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
