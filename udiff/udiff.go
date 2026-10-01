// Package udiff renders and parses unified-diff text.
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

// ErrFormat classifies malformed patch text; ParseError unwraps to it.
var ErrFormat = errors.New("udiff: malformed patch")

// ParseError reports a format problem at a 1-based patch text line.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return fmt.Sprintf("udiff: line %d: %s", e.Line, e.Msg) }
func (e *ParseError) Unwrap() error { return ErrFormat }

const noNewline = "\\ No newline at end of file\n"

// Diff renders the diff turning a into b; maxDist <= 0 means no limit.
func Diff(a, b []byte, ctx, maxDist int) ([]byte, error) {
	la, lb := lines.Split(a), lines.Split(b)
	s, err := edit.Diff(la, lb, maxDist)
	if err != nil {
		return nil, err
	}
	return Render(hunk.Group(s, la, lb, ctx)), nil
}

func Render(hs []hunk.Hunk) []byte {
	var w bytes.Buffer
	w.WriteString("--- a\n+++ b\n")
	for _, h := range hs {
		fmt.Fprintf(&w, "@@ -%s +%s @@\n", rng(h.AStart, h.ACount), rng(h.BStart, h.BCount))
		for _, l := range h.Lines {
			w.WriteByte(l.Kind)
			w.Write(l.Text)
			if !bytes.HasSuffix(l.Text, []byte("\n")) {
				w.WriteString("\n" + noNewline)
			}
		}
	}
	return w.Bytes()
}

func rng(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Parse strictly validates unified-diff text: declared hunk counts
// must match the body exactly.
func Parse(data []byte) ([]hunk.Hunk, error) {
	ls := lines.Split(data)
	fail := func(i int, msg string) error { return &ParseError{Line: i + 1, Msg: msg} }
	at := func(i int) string { return string(ls[i]) }
	if len(ls) < 2 || !strings.HasPrefix(at(0), "--- ") || !strings.HasPrefix(at(1), "+++ ") {
		return nil, fail(0, "missing ---/+++ header")
	}
	var hs []hunk.Hunk
	for i := 2; i < len(ls); {
		h, ok := parseHeader(strings.TrimSuffix(at(i), "\n"))
		if !ok {
			return nil, fail(i, "expected @@ hunk header")
		}
		i++
		old, new_ := 0, 0
		for old < h.ACount || new_ < h.BCount {
			if i >= len(ls) {
				return nil, fail(i, "hunk body truncated")
			}
			raw := at(i)
			if !strings.HasSuffix(raw, "\n") {
				return nil, fail(i, "unterminated line")
			}
			k := raw[0]
			if k != ' ' && k != '-' && k != '+' {
				return nil, fail(i, "bad line prefix")
			}
			if old += b2i(k != '+'); old > h.ACount {
				return nil, fail(i, "more old-side lines than declared")
			}
			if new_ += b2i(k != '-'); new_ > h.BCount {
				return nil, fail(i, "more new-side lines than declared")
			}
			text := []byte(raw[1:])
			if i+1 < len(ls) && at(i+1) == noNewline {
				text = text[:len(text)-1]
				i++
			}
			h.Lines = append(h.Lines, hunk.Line{Kind: k, Text: text})
			i++
		}
		hs = append(hs, h)
	}
	return hs, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func parseHeader(s string) (hunk.Hunk, bool) {
	var h hunk.Hunk
	rest, ok := strings.CutPrefix(s, "@@ -")
	if !ok {
		return h, false
	}
	num := func() (int, bool) {
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		n, err := strconv.Atoi(rest[:i])
		rest = rest[i:]
		return n, err == nil
	}
	part := func() (int, int, bool) {
		n, ok := num()
		c := 1
		if r, hit := strings.CutPrefix(rest, ","); hit {
			rest = r
			c, ok = num()
		}
		return n, c, ok
	}
	if h.AStart, h.ACount, ok = part(); !ok || !strings.HasPrefix(rest, " +") {
		return h, false
	}
	rest = rest[2:]
	if h.BStart, h.BCount, ok = part(); !ok || !strings.HasPrefix(rest, " @@") {
		return h, false
	}
	return h, true
}
