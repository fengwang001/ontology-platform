// Package udiff renders hunks as unified-diff text and parses it back
// strictly, rejecting malformed input with line numbers attached.
package udiff

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

var ErrFormat = errors.New("udiff: bad patch format") // errors carry line no.

// Diff computes the unified diff of a and b with ctx context lines.
func Diff(a, b []byte, ctx int) []byte {
	ops, _ := edit.Diff(lines.Split(a), lines.Split(b), -1) // unlimited: never fails
	return Render("a", "b", hunk.Build(ops, ctx))
}

// Render formats hunks with --- / +++ file headers.
func Render(oldName, newName string, hs []hunk.Hunk) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", oldName, newName)
	for _, h := range hs {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", rng(h.OldStart, h.OldCount), rng(h.NewStart, h.NewCount))
		for _, l := range h.Lines {
			b.WriteByte(l.Kind)
			b.WriteString(l.Text)
			if !strings.HasSuffix(l.Text, "\n") {
				b.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return b.Bytes()
}

func rng(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Parse strictly parses unified-diff text into hunks.
func Parse(p []byte) ([]hunk.Hunk, error) {
	var ls []string
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			ls = append(ls, string(p))
			break
		}
		ls = append(ls, string(p[:i]))
		p = p[i+1:]
	}
	bad := func(n int, m string) error { return fmt.Errorf("%w: line %d: %s", ErrFormat, n, m) }
	stripNL := func(h *hunk.Hunk) {
		last := &h.Lines[len(h.Lines)-1]
		last.Text = strings.TrimSuffix(last.Text, "\n")
	}
	if len(ls) < 1 || !strings.HasPrefix(ls[0], "--- ") {
		return nil, bad(1, "missing --- header")
	}
	if len(ls) < 2 || !strings.HasPrefix(ls[1], "+++ ") {
		return nil, bad(2, "missing +++ header")
	}
	var hs []hunk.Hunk
	i := 2
	for i < len(ls) {
		h, ok := parseHeader(ls[i])
		if !ok {
			return nil, bad(i+1, "bad hunk header")
		}
		i++
		oldNeed, newNeed := h.OldCount, h.NewCount
		for oldNeed+newNeed > 0 {
			if i >= len(ls) {
				return nil, bad(i+1, "unexpected end of patch")
			}
			l := ls[i]
			i++
			if l == `\ No newline at end of file` {
				if len(h.Lines) == 0 {
					return nil, bad(i, "stray no-newline marker")
				}
				stripNL(&h)
				continue
			}
			if len(l) == 0 || (l[0] != ' ' && l[0] != '-' && l[0] != '+') {
				return nil, bad(i, "bad line prefix")
			}
			if l[0] != '+' {
				oldNeed--
			}
			if l[0] != '-' {
				newNeed--
			}
			if oldNeed < 0 || newNeed < 0 {
				return nil, bad(i, "too many lines in hunk")
			}
			h.Lines = append(h.Lines, hunk.Line{Kind: l[0], Text: l[1:] + "\n"})
		}
		if i < len(ls) && ls[i] == `\ No newline at end of file` && len(h.Lines) > 0 {
			stripNL(&h)
			i++
		}
		hs = append(hs, h)
	}
	return hs, nil
}

func parseHeader(l string) (h hunk.Hunk, ok bool) {
	plus := strings.Index(l, " +")
	if !strings.HasPrefix(l, "@@ -") || !strings.HasSuffix(l, " @@") || plus < 4 {
		return h, false
	}
	mid := l[4 : len(l)-3]
	var ok1, ok2 bool
	h.OldStart, h.OldCount, ok1 = parseRange(mid[:plus-4])
	h.NewStart, h.NewCount, ok2 = parseRange(mid[plus-4+2:])
	return h, ok1 && ok2
}

func parseRange(s string) (start, count int, ok bool) {
	if i := strings.IndexByte(s, ','); i >= 0 {
		a, ok1 := atoi(s[:i])
		b, ok2 := atoi(s[i+1:])
		return a, b, ok1 && ok2
	}
	start, ok = atoi(s)
	return start, 1, ok
}

func atoi(s string) (int, bool) {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, s != ""
}
