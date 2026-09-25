// Package udiff renders and parses unified-diff text: ---/+++ headers,
// @@ -a,b +c,d @@ hunk headers, space/-/+ content and the
// "\ No newline at end of file" marker. Parsing is strict: declared counts
// must match the actual rows exactly.
package udiff

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ontology/hunk"
	"ontology/lines"
)

var (
	// ErrFormat classifies any syntactically malformed patch text.
	ErrFormat = errors.New("udiff: malformed patch")
	// ErrLimit classifies a patch exceeding the configured resource limits.
	ErrLimit = errors.New("udiff: patch exceeds limits")
)

// FormatError carries the 1-based patch line where parsing failed.
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string { return fmt.Sprintf("%s: line %d: %s", ErrFormat, e.Line, e.Msg) }
func (e *FormatError) Unwrap() error { return ErrFormat }

// File is one ---/+++ document with its hunks.
type File struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}

// Limits caps accepted patch size. A zero value means unlimited.
type Limits struct {
	MaxBytes int
	MaxHunks int
}

const noNL = "\\ No newline at end of file"

func rng(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// Render serializes f to canonical unified-diff text.
func Render(f *File) []byte {
	var b []byte
	b = append(b, "--- "+f.OldName+"\n"...)
	b = append(b, "+++ "+f.NewName+"\n"...)
	for _, h := range f.Hunks {
		b = append(b, "@@ -"+rng(h.OldStart, h.OldCount)+" +"+rng(h.NewStart, h.NewCount)+" @@\n"...)
		for _, l := range h.Lines {
			b = append(b, l.Kind)
			b = append(b, l.Text...)
			if !lines.HasNL(l.Text) {
				b = append(b, '\n')
				b = append(b, noNL+"\n"...)
			}
		}
	}
	return b
}

// Diff builds a File from two raw documents.
func Diff(oldName, newName string, a, b []byte, ctx, maxDist int) (*File, error) {
	hs, err := hunk.Make(lines.Split(a), lines.Split(b), ctx, maxDist)
	if err != nil {
		return nil, err
	}
	return &File{OldName: oldName, NewName: newName, Hunks: hs}, nil
}

func ferr(line int, msg string) error { return &FormatError{Line: line, Msg: msg} }

func parseRange(s string) (int, int, error) {
	v, err := strconv.Atoi(s)
	if err == nil {
		return v, 1, nil
	}
	lo, hi, ok := strings.Cut(s, ",")
	if !ok {
		return 0, 0, err
	}
	start, e1 := strconv.Atoi(lo)
	count, e2 := strconv.Atoi(hi)
	if e1 != nil || e2 != nil {
		return 0, 0, err
	}
	return start, count, nil
}

// Parse strictly parses unified-diff text under lim.
func Parse(data []byte, lim Limits) (*File, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, fmt.Errorf("%w: %d > %d bytes", ErrLimit, len(data), lim.MaxBytes)
	}
	ls := lines.Split(data)
	headerOK := func(l []byte, p string) bool {
		return len(l) >= len(p)+1 && l[len(l)-1] == '\n' && strings.HasPrefix(string(l), p)
	}
	if len(ls) < 2 || !headerOK(ls[0], "--- ") || !headerOK(ls[1], "+++ ") {
		return nil, ferr(1, "expected --- and +++ headers")
	}
	f := &File{OldName: string(ls[0][4 : len(ls[0])-1]), NewName: string(ls[1][4 : len(ls[1])-1])}
	i := 2
	for i < len(ls) {
		lineNo := i + 1
		fields := strings.Fields(string(ls[i]))
		if len(fields) < 4 || fields[0] != "@@" || fields[3] != "@@" ||
			!strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
			return nil, ferr(lineNo, "expected hunk header")
		}
		os, oc, e1 := parseRange(fields[1][1:])
		ns, nc, e2 := parseRange(fields[2][1:])
		if e1 != nil || e2 != nil || oc < 0 || nc < 0 {
			return nil, ferr(lineNo, "bad hunk range")
		}
		h := hunk.Hunk{OldStart: os, OldCount: oc, NewStart: ns, NewCount: nc}
		i++
		oldN, newN, prevContent := 0, 0, false
		trailingMarker := func() bool {
			return i < len(ls) && string(ls[i]) == noNL+"\n"
		}
		for oldN < oc || newN < nc || prevContent && trailingMarker() {
			if i >= len(ls) {
				return nil, ferr(len(ls)+1, "unexpected end: missing hunk rows")
			}
			raw := ls[i]
			if len(raw) == 0 || raw[len(raw)-1] != '\n' {
				return nil, ferr(i+1, "unterminated row (truncated patch?)")
			}
			switch raw[0] {
			case '\\':
				if !prevContent || string(raw) != noNL+"\n" {
					return nil, ferr(i+1, "stray no-newline marker")
				}
				t := h.Lines[len(h.Lines)-1].Text
				if len(t) == 0 || t[len(t)-1] != '\n' {
					return nil, ferr(i+1, "marker without terminated row")
				}
				h.Lines[len(h.Lines)-1].Text = t[:len(t)-1]
			case ' ':
				oldN++
				newN++
				h.Lines = append(h.Lines, hunk.Line{Kind: ' ', Text: raw[1:]})
			case '-':
				oldN++
				h.Lines = append(h.Lines, hunk.Line{Kind: '-', Text: raw[1:]})
			case '+':
				newN++
				h.Lines = append(h.Lines, hunk.Line{Kind: '+', Text: raw[1:]})
			default:
				return nil, ferr(i+1, "row must start with space, -, + or \\")
			}
			prevContent = raw[0] != '\\'
			i++
			if oldN > oc || newN > nc {
				return nil, ferr(i, "more rows than header declares")
			}
		}
		f.Hunks = append(f.Hunks, h)
		if lim.MaxHunks > 0 && len(f.Hunks) > lim.MaxHunks {
			return nil, fmt.Errorf("%w: %d > %d hunks", ErrLimit, len(f.Hunks), lim.MaxHunks)
		}
	}
	return f, nil
}
