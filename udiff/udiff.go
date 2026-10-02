// Package udiff renders hunks to unified diff text and parses it back
// strictly, preserving line terminators and no-newline markers.
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

// ErrFormat marks a malformed patch; the message carries the 1-based
// line number in the patch text.
var ErrFormat = errors.New("udiff: malformed patch")

// Diff computes hunks between two byte strings with ctx context lines.
func Diff(a, b []byte, ctx int) ([]hunk.Hunk, error) {
	la, lb := lines.Split(a), lines.Split(b)
	ops, err := edit.Diff(la, lb, 0)
	if err != nil {
		return nil, err
	}
	return hunk.Build(la, lb, ops, ctx), nil
}

// Render formats hunks as unified diff text with the given file names.
func Render(oldName, newName string, hs []hunk.Hunk) string {
	var sb strings.Builder
	sb.WriteString("--- " + oldName + "\n+++ " + newName + "\n")
	for _, h := range hs {
		sb.WriteString("@@ -" + span(h.OldStart, h.OldCount) + " +" + span(h.NewStart, h.NewCount) + " @@\n")
		for _, l := range h.Lines {
			sb.WriteByte(l.Kind)
			sb.WriteString(l.Text)
			if !strings.HasSuffix(l.Text, "\n") {
				sb.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return sb.String()
}

// span formats a hunk range: 0-based start + count -> "a[,b]".
func span(start0, count int) string {
	s := strconv.Itoa(start0 + min(count, 1))
	if count != 1 {
		s += "," + strconv.Itoa(count)
	}
	return s
}

// Parse strictly parses unified diff text into hunks.
func Parse(text string) ([]hunk.Hunk, error) {
	ls := lines.Split([]byte(text))
	pos := 0
	next := func() (string, int) {
		if pos >= len(ls) {
			return "", -1
		}
		pos++
		return ls[pos-1], pos
	}
	var l string
	var n int
	for _, prefix := range []string{"--- ", "+++ "} {
		if l, n = next(); n < 0 || !strings.HasPrefix(l, prefix) {
			return nil, errf(n, "expected "+prefix+"header")
		}
	}
	var hs []hunk.Hunk
	for {
		if l, n = next(); n < 0 {
			return hs, nil
		}
		h, err := parseHeader(l, n)
		if err != nil {
			return nil, err
		}
		oldSeen, newSeen, last := 0, 0, -1
		for oldSeen < h.OldCount || newSeen < h.NewCount {
			if l, n = next(); n < 0 || !strings.HasSuffix(l, "\n") {
				return nil, errf(n, "hunk has fewer lines than declared")
			}
			switch l[0] {
			case ' ', '-', '+':
				if l[0] != '+' {
					oldSeen++
				}
				if l[0] != '-' {
					newSeen++
				}
				if oldSeen > h.OldCount || newSeen > h.NewCount {
					return nil, errf(n, "hunk has more lines than declared")
				}
				h.Lines = append(h.Lines, hunk.Line{Kind: l[0], Text: l[1:]})
				last = len(h.Lines) - 1
			case '\\':
				if l != "\\ No newline at end of file\n" || last < 0 {
					return nil, errf(n, "bad no-newline marker")
				}
				h.Lines[last].Text = strings.TrimSuffix(h.Lines[last].Text, "\n")
			default:
				return nil, errf(n, "bad line prefix")
			}
		}
		hs = append(hs, h)
	}
}

func errf(n int, msg string) error {
	return fmt.Errorf("%w: line %d: %s", ErrFormat, max(n, 1), msg)
}

func parseHeader(l string, n int) (hunk.Hunk, error) {
	var h hunk.Hunk
	s := strings.TrimSuffix(l, "\n")
	if !strings.HasPrefix(s, "@@ -") || !strings.Contains(s, " @@") {
		return h, errf(n, "expected @@ hunk header")
	}
	parts := strings.Split(s[4:strings.Index(s, " @@")], " +")
	if len(parts) != 2 {
		return h, errf(n, "bad hunk header ranges")
	}
	nums := [4]int{}
	for i, p := range parts {
		f := strings.Split(p, ",")
		v, err := strconv.Atoi(f[0])
		c := 1
		if len(f) == 2 {
			c, err = strconv.Atoi(f[1])
		} else if len(f) > 2 {
			err = strconv.ErrSyntax
		}
		if err != nil || v < 0 || c < 0 || (c > 0 && v == 0) {
			return h, errf(n, "bad hunk range")
		}
		nums[2*i], nums[2*i+1] = v, c
	}
	h.OldStart, h.OldCount = start0(nums[0], nums[1]), nums[1]
	h.NewStart, h.NewCount = start0(nums[2], nums[3]), nums[3]
	return h, nil
}

// start0 converts a written range number back to a 0-based index.
func start0(num, count int) int { return num - min(count, 1) }
