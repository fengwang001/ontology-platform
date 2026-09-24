package udiff

import (
	"fmt"
	"strconv"
	"strings"

	"ontology/edit"
)

func pr(s string) (int, int, error) {
	t := s[1:]
	ai := func(x string) (int, bool) {
		v, e := strconv.Atoi(x)
		return v, e == nil && v >= 0
	}
	if c := strings.IndexByte(t, ','); c >= 0 {
		a, o1 := ai(t[:c])
		d, o2 := ai(t[c+1:])
		if !o1 || !o2 {
			return 0, 0, fmt.Errorf("bad range %q", s)
		}
		return a, d, nil
	}
	a, ok := ai(t)
	if !ok {
		return 0, 0, fmt.Errorf("bad range %q", s)
	}
	return a, 1, nil
}

func eolOf(t string, no bool) []byte {
	if no {
		return nil
	}
	if strings.HasSuffix(t, "\r") {
		return []byte("\r\n")
	}
	return []byte{'\n'}
}

// Parse strictly parses diff bytes; counts must match the body exactly.
func Parse(data []byte) (*Patch, error) {
	if len(data) == 0 {
		return nil, bad(1, "empty patch")
	}
	ls := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(ls) < 2 || ls[0][:4] != "--- " || ls[1][:4] != "+++ " {
		return nil, bad(1, "expected ---/+++ headers")
	}
	p := &Patch{OldLabel: ls[0][4:], NewLabel: ls[1][4:]}
	for i := 2; i < len(ls); {
		f := strings.Fields(ls[i])
		if len(f) != 4 || f[0] != "@@" || f[3] != "@@" || f[1][0] != '-' || f[2][0] != '+' {
			return nil, bad(i+1, "malformed hunk header")
		}
		s0, n0, e1 := pr(f[1])
		s1, n1, e2 := pr(f[2])
		if e1 != nil || e2 != nil {
			return nil, bad(i+1, "bad range")
		}
		h := &Hunk{S0: s0, N0: n0, S1: s1, N1: n1}
		i++
		for c0, c1 := 0, 0; c0 < n0 || c1 < n1; {
			if i >= len(ls) {
				return nil, bad(i+1, "truncated hunk body")
			}
			l := ls[i]
			i++
			r := Row{Text: l[1:]}
			switch l[0] {
			case ' ':
				r.Kind, c0, c1 = edit.Equal, c0+1, c1+1
			case '-':
				r.Kind, c0 = edit.Delete, c0+1
			case '+':
				r.Kind, c1 = edit.Insert, c1+1
			default:
				return nil, bad(i, "bad row prefix")
			}
			if i < len(ls) && ls[i] == nonl {
				r.OldNoNL, r.NewNoNL = r.Kind != edit.Insert, r.Kind != edit.Delete
				i++
			}
			h.Rows = append(h.Rows, r)
		}
		for ri := range h.Rows {
			r := &h.Rows[ri]
			if r.Kind != edit.Insert {
				r.OldEOL = eolOf(r.Text, r.OldNoNL)
			}
			if r.Kind != edit.Delete {
				r.NewEOL = eolOf(r.Text, r.NewNoNL)
			}
		}
		p.Hunks = append(p.Hunks, h)
	}
	return p, nil
}
