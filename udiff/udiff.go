// Package udiff renders and strictly parses unified diff text.
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

// ErrFormat marks malformed patch text; *FormatError adds the line number.
var ErrFormat = errors.New("udiff: malformed patch")

// ErrLimit marks a patch exceeding configured byte or hunk limits.
var ErrLimit = errors.New("udiff: patch exceeds configured limits")

// FormatError reports a parse error at a 1-based line of the patch text.
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string { return fmt.Sprintf("udiff: line %d: %s", e.Line, e.Msg) }
func (e *FormatError) Unwrap() error { return ErrFormat }

// Patch is a parsed unified diff for one document.
type Patch struct {
	Hunks []hunk.Hunk
}

// Diff computes the shortest patch turning a into b with ctx context lines.
func Diff(a, b []byte, ctx, maxD int) (*Patch, error) {
	la, lb := lines.Split(a), lines.Split(b)
	s, err := edit.Diff(la, lb, maxD)
	if err != nil {
		return nil, err
	}
	return &Patch{Hunks: hunk.Group(la, lb, s, ctx)}, nil
}

// Render formats p as unified diff text. An empty patch renders empty.
func Render(p *Patch) []byte {
	var out bytes.Buffer
	if len(p.Hunks) > 0 {
		out.WriteString("--- a\n+++ b\n")
	}
	for _, h := range p.Hunks {
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", span(h.AStart, h.ACount), span(h.BStart, h.BCount))
		for _, l := range h.Lines {
			out.WriteByte(l.Kind)
			out.Write(l.Text)
			if !bytes.HasSuffix(l.Text, []byte("\n")) {
				out.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return out.Bytes()
}

// span formats one range from 0-based start s and count c (DESIGN.md §1).
func span(s, c int) string {
	if c == 0 {
		return strconv.Itoa(s) + ",0"
	}
	if c == 1 {
		return strconv.Itoa(s + 1)
	}
	return strconv.Itoa(s+1) + "," + strconv.Itoa(c)
}

// Parse reads unified diff text; maxBytes/maxHunks <= 0 mean no limit.
// Declared hunk counts must match the actual lines exactly.
func Parse(p []byte, maxBytes, maxHunks int) (*Patch, error) {
	if maxBytes > 0 && len(p) > maxBytes {
		return nil, ErrLimit
	}
	ls := lines.Split(p)
	if len(ls) == 0 {
		return &Patch{}, nil
	}
	if !bytes.HasPrefix(ls[0], []byte("--- ")) {
		return nil, &FormatError{1, "expected --- header"}
	}
	if len(ls) < 2 || !bytes.HasPrefix(ls[1], []byte("+++ ")) {
		return nil, &FormatError{2, "expected +++ header"}
	}
	var patch Patch
	i := 2
	for i < len(ls) {
		if maxHunks > 0 && len(patch.Hunks) >= maxHunks {
			return nil, ErrLimit
		}
		h, err := parseHunk(ls, &i)
		if err != nil {
			return nil, err
		}
		patch.Hunks = append(patch.Hunks, h)
	}
	return &patch, nil
}

func parseHunk(ls [][]byte, i *int) (hunk.Hunk, error) {
	var h hunk.Hunk
	bad := func(msg string) (hunk.Hunk, error) { return h, &FormatError{*i + 1, msg} }
	r, ok := strings.CutPrefix(strings.TrimSuffix(string(ls[*i]), "\n"), "@@ -")
	if !ok {
		return bad("expected @@ hunk header")
	}
	var aStart, aCount, bStart, bCount int
	if aStart, aCount, r, ok = parseSpan(r); !ok {
		return bad("bad old range")
	}
	if r, ok = strings.CutPrefix(r, " +"); !ok {
		return bad("expected + range")
	}
	if bStart, bCount, r, ok = parseSpan(r); !ok || r != " @@" {
		return bad("bad new range")
	}
	h.AStart, h.BStart = aStart, bStart // count 0: start is the 0-based index
	if aCount > 0 {
		h.AStart--
	}
	if bCount > 0 {
		h.BStart--
	}
	*i++
	for *i < len(ls) {
		l := ls[*i]
		if l[0] == '\\' {
			if string(l) != "\\ No newline at end of file\n" || len(h.Lines) == 0 {
				return bad("bad no-newline marker")
			}
			t := h.Lines[len(h.Lines)-1].Text
			if !bytes.HasSuffix(t, []byte("\n")) {
				return bad("duplicate no-newline marker")
			}
			h.Lines[len(h.Lines)-1].Text = t[:len(t)-1]
			*i++
			continue
		}
		if h.ACount == aCount && h.BCount == bCount {
			break
		}
		switch l[0] {
		case ' ':
			if h.ACount >= aCount || h.BCount >= bCount {
				return bad("more lines than declared")
			}
			h.ACount++
			h.BCount++
		case '-':
			if h.ACount >= aCount {
				return bad("more old lines than declared")
			}
			h.ACount++
		case '+':
			if h.BCount >= bCount {
				return bad("more new lines than declared")
			}
			h.BCount++
		default:
			return bad("line must start with ' ', '-' or '+'")
		}
		h.Lines = append(h.Lines, hunk.Line{Kind: l[0], Text: l[1:]})
		*i++
	}
	if h.ACount != aCount || h.BCount != bCount {
		return bad("hunk has fewer lines than declared")
	}
	return h, nil
}

// parseSpan parses "a[,b]" from the front of s: start, count, rest.
func parseSpan(s string) (start, count int, rest string, ok bool) {
	if start, rest, ok = eatNum(s); !ok {
		return 0, 0, s, false
	}
	count = 1
	if r, yes := strings.CutPrefix(rest, ","); yes {
		if count, rest, ok = eatNum(r); !ok {
			return 0, 0, s, false
		}
	}
	return start, count, rest, true
}

func eatNum(s string) (int, string, bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, s, false
	}
	n, err := strconv.Atoi(s[:i])
	return n, s[i:], err == nil
}
