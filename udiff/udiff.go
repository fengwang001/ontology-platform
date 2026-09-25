// Package udiff renders and parses unified diff text.
package udiff

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

// File is a unified diff for one pair of files.
type File struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}

// ErrFormat marks malformed patch text; all parse failures match it.
var ErrFormat = errors.New("udiff: format error")

// ParseError reports a format problem at a 1-based patch line number.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("udiff: line %d: %s", e.Line, e.Msg)
}

// Is lets errors.Is(err, ErrFormat) succeed for ParseError.
func (e *ParseError) Is(t error) bool { return t == ErrFormat }

// Diff computes the unified diff between a and b with ctx context
// lines and an optional edit-distance limit.
func Diff(a, b []byte, ctx, maxDist int) (*File, error) {
	la, lb := lines.Split(a), lines.Split(b)
	ops, err := edit.Diff(la, lb, maxDist)
	if err != nil {
		return nil, err
	}
	return &File{OldName: "a", NewName: "b", Hunks: hunk.Group(la, lb, ops, ctx)}, nil
}

// Render emits f as unified diff text.
func Render(f *File) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--- %s\n+++ %s\n", f.OldName, f.NewName)
	for _, h := range f.Hunks {
		fmt.Fprintf(&buf, "@@ -%s +%s @@\n", span(h.OldStart, h.OldCount), span(h.NewStart, h.NewCount))
		for _, l := range h.Lines {
			buf.WriteByte(l.Kind)
			buf.Write(l.Text)
			if !bytes.HasSuffix(l.Text, []byte("\n")) {
				buf.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return buf.Bytes()
}

func span(s, c int) string {
	if c == 1 {
		return strconv.Itoa(s)
	}
	return strconv.Itoa(s) + "," + strconv.Itoa(c)
}

var hdrRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@( .*)?$`)

// Parse reads unified diff text strictly: declared hunk counts must
// match the body exactly. maxBytes and maxHunks, when positive, cap
// the patch size and hunk count.
func Parse(data []byte, maxBytes, maxHunks int) (*File, error) {
	if maxBytes > 0 && len(data) > maxBytes {
		return nil, &ParseError{Line: 1, Msg: "patch exceeds byte limit"}
	}
	ls := lines.Split(data)
	fail := func(i int, msg string) (*File, error) {
		return nil, &ParseError{Line: i + 1, Msg: msg}
	}
	if len(ls) < 2 || !bytes.HasPrefix(ls[0], []byte("--- ")) {
		return fail(0, "missing --- header")
	}
	if !bytes.HasPrefix(ls[1], []byte("+++ ")) {
		return fail(1, "missing +++ header")
	}
	name := func(l []byte) string { return string(bytes.TrimSuffix(l[4:], []byte("\n"))) }
	f := &File{OldName: name(ls[0]), NewName: name(ls[1])}
	i := 2
	for i < len(ls) {
		m := hdrRe.FindSubmatch(bytes.TrimSuffix(ls[i], []byte("\n")))
		if m == nil {
			return fail(i, "expected hunk header")
		}
		h := hunk.Hunk{OldStart: num(m[1]), OldCount: cnt(m[2]), NewStart: num(m[3]), NewCount: cnt(m[4])}
		i++
		needOld, needNew, prev := h.OldCount, h.NewCount, -1
		for needOld+needNew > 0 || (i < len(ls) && len(ls[i]) > 0 && ls[i][0] == '\\') {
			if i >= len(ls) {
				return fail(i, "truncated hunk body")
			}
			l := ls[i]
			if l[0] == '\\' {
				if prev < 0 {
					return fail(i, "stray no-newline marker")
				}
				h.Lines[prev].Text = bytes.TrimSuffix(h.Lines[prev].Text, []byte("\n"))
				i++
				continue
			}
			if l[0] != ' ' && l[0] != '-' && l[0] != '+' {
				return fail(i, "bad line prefix")
			}
			if l[0] != '+' {
				needOld--
			}
			if l[0] != '-' {
				needNew--
			}
			if needOld < 0 || needNew < 0 {
				return fail(i, "hunk body exceeds declared counts")
			}
			h.Lines = append(h.Lines, hunk.Line{Kind: l[0], Text: l[1:]})
			prev = len(h.Lines) - 1
			i++
		}
		if maxHunks > 0 && len(f.Hunks)+1 > maxHunks {
			return fail(i, "too many hunks")
		}
		f.Hunks = append(f.Hunks, h)
	}
	return f, nil
}

func num(b []byte) int { n, _ := strconv.Atoi(string(b)); return n }

func cnt(b []byte) int {
	if len(b) == 0 {
		return 1
	}
	return num(b)
}
