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

var ErrFormat = errors.New("udiff: malformed patch")

const noNL = `\ No newline at end of file`

var bodyTab = []struct {
	pref string
	oo   int
	nn   int
	k    edit.Kind
}{{" ", 1, 1, edit.Equal}, {"-", 1, 0, edit.Delete}, {"+", 0, 1, edit.Insert}}

type Limits struct{ MaxBytes, MaxHunks int }
type Patch struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string   { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }
func (e *FormatError) Is(t error) bool { return t == ErrFormat }

func Render(p *Patch) []byte {
	var sb strings.Builder
	sb.WriteString("--- " + p.OldName + "\n+++ " + p.NewName + "\n")
	for _, h := range p.Hunks {
		fmt.Fprintf(&sb, "@@ -%s +%s @@\n", rng(h.OldStart, h.OldCount), rng(h.NewStart, h.NewCount))
		for _, o := range h.Ops {
			sb.WriteByte([]byte{' ', '-', '+'}[o.Kind])
			sb.WriteString(o.Line.Text + o.Line.NL)
			if o.Line.NL == "" {
				sb.WriteString("\n" + noNL + "\n")
			}
		}
	}
	return []byte(sb.String())
}

func Parse(data []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, &FormatError{1, "patch exceeds byte limit"}
	}
	ls, p := lines.Split(data), &Patch{}
	i := 0
	fail := func(m string) error { return &FormatError{i, m} }
	heads := []struct {
		pref string
		dst  *string
	}{{"--- ", &p.OldName}, {"+++ ", &p.NewName}}
	for _, hd := range heads {
		ok := i < len(ls) && strings.HasPrefix(ls[i].Text, hd.pref) && ls[i].NL != ""
		if !ok {
			return nil, fail("bad file header")
		}
		*hd.dst, i = ls[i].Text[4:], i+1
	}
	for i < len(ls) {
		if lim.MaxHunks > 0 && len(p.Hunks) >= lim.MaxHunks {
			return nil, fail("too many hunks")
		}
		h, err := parseHunk(ls, &i, fail)
		if err != nil {
			return nil, err
		}
		p.Hunks = append(p.Hunks, h)
	}
	if len(p.Hunks) == 0 {
		return nil, &FormatError{i, "no hunks"}
	}
	return p, nil
}

func rng(s, n int) string {
	if n == 1 {
		return strconv.Itoa(s)
	}
	return strconv.Itoa(s) + "," + strconv.Itoa(n)
}

func parseHunk(ls []lines.Line, ip *int, fail func(string) error) (hunk.Hunk, error) {
	hl := ls[*ip]
	*ip++
	if !strings.HasPrefix(hl.Text, "@@ ") || hl.NL == "" {
		return hunk.Hunk{}, fail("missing/bad hunk header")
	}
	q := strings.SplitN(strings.TrimSuffix(strings.TrimPrefix(hl.Text, "@@ "), " @@"), " ", 2)
	os, oc, e1 := pr(q[0])
	ns, nc := 0, 0
	var e2 error
	if len(q) == 2 {
		ns, nc, e2 = pr(q[1])
	}
	if len(q) != 2 || e1 != nil || e2 != nil {
		return hunk.Hunk{}, fail("bad hunk range")
	}
	h := hunk.Hunk{OldStart: os, OldCount: oc, NewStart: ns, NewCount: nc}
	oi, ni, prev := 0, 0, -1
	for oi < oc || ni < nc {
		if *ip >= len(ls) {
			return hunk.Hunk{}, fail("truncated hunk body")
		}
		l, t := ls[*ip], ls[*ip].Text
		*ip++
		if strings.HasPrefix(t, `\`) {
			if prev < 0 || t != noNL {
				return hunk.Hunk{}, fail("bad no-newline marker")
			}
			h.Ops[prev].Line.NL = ""
			continue
		}
		row := -1
		for r := range bodyTab {
			if strings.HasPrefix(t, bodyTab[r].pref) {
				row = r
				break
			}
		}
		if row < 0 || oi+bodyTab[row].oo > oc || ni+bodyTab[row].nn > nc {
			return hunk.Hunk{}, fail("bad/extra body line")
		}
		h.Ops = append(h.Ops, edit.Op{Kind: bodyTab[row].k, Line: lines.Line{Text: t[1:], NL: l.NL}})
		oi, ni, prev = oi+bodyTab[row].oo, ni+bodyTab[row].nn, len(h.Ops)-1
	}
	return h, nil
}

func pr(s string) (int, int, error) {
	if len(s) < 2 {
		return 0, 0, ErrFormat
	}
	body, n := s[1:], 1
	if j := strings.IndexByte(body, ','); j >= 0 {
		c, err := strconv.Atoi(body[j+1:])
		if err != nil || c < 0 {
			return 0, 0, ErrFormat
		}
		n, body = c, body[:j]
	}
	st, err := strconv.Atoi(body)
	if err != nil || st < 0 || n > 0 && st < 1 {
		return 0, 0, ErrFormat
	}
	return st, n, nil
}
