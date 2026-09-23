// Package udiff renders and parses unified diffs. Parsing is strict:
// declared hunk counts must match the actual lines exactly.
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

// ErrFormat classifies malformed patches (including limit violations).
var ErrFormat = errors.New("udiff: malformed patch")

// FormatError reports a malformed patch at a 1-based line number.
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string { return fmt.Sprintf("udiff: line %d: %s", e.Line, e.Msg) }
func (e *FormatError) Unwrap() error { return ErrFormat }

// Limits caps accepted patches; zero fields mean unlimited.
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// FilePatch is a parsed or generated patch for one file.
type FilePatch struct {
	OldPath, NewPath string
	Hunks            []hunk.Hunk
}

const marker = `\ No newline at end of file`

// Diff computes hunks between a and b with the given context size.
func Diff(oldPath, newPath string, a, b []byte, context int) (FilePatch, error) {
	ops, err := new(edit.Differ).Diff(lines.Split(a), lines.Split(b))
	if err != nil {
		return FilePatch{}, err
	}
	return FilePatch{oldPath, newPath, hunk.Build(ops, context)}, nil
}

// Render formats p as unified diff text, preserving line endings exactly.
func Render(p FilePatch) []byte {
	var sb strings.Builder
	sb.WriteString("--- " + p.OldPath + "\n+++ " + p.NewPath + "\n")
	for _, h := range p.Hunks {
		sb.WriteString("@@ -" + span(h.OldStart, h.OldCount) + " +" + span(h.NewStart, h.NewCount) + " @@\n")
		for _, e := range h.Entries {
			prefix := " "
			if e.Kind == edit.Del {
				prefix = "-"
			} else if e.Kind == edit.Ins {
				prefix = "+"
			}
			sb.WriteString(prefix + e.Line.Text)
			if e.Line.End == "" {
				sb.WriteString("\n" + marker + "\n")
			} else {
				sb.WriteString(e.Line.End)
			}
		}
	}
	return []byte(sb.String())
}

func span(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Parse reads unified diff text, enforcing declared counts and limits.
func Parse(text []byte, lim Limits) (FilePatch, error) {
	if lim.MaxBytes > 0 && len(text) > lim.MaxBytes {
		return FilePatch{}, &FormatError{Line: 1, Msg: "patch exceeds byte limit"}
	}
	ls := lines.Split(text)
	fail := func(i int, msg string) (FilePatch, error) {
		return FilePatch{}, &FormatError{Line: i + 1, Msg: msg}
	}
	if len(ls) < 2 || !strings.HasPrefix(ls[0].Text, "--- ") || !strings.HasPrefix(ls[1].Text, "+++ ") {
		return fail(0, "missing ---/+++ header")
	}
	p := FilePatch{OldPath: ls[0].Text[4:], NewPath: ls[1].Text[4:]}
	i := 2
	for i < len(ls) {
		h, err := parseHeader(ls[i].Text)
		if err != nil {
			return fail(i, err.Error())
		}
		i++
		needOld, needNew := h.OldCount, h.NewCount
		for needOld > 0 || needNew > 0 {
			if i >= len(ls) {
				return fail(i, "unexpected end of patch inside hunk")
			}
			t := ls[i].Text
			if t == marker {
				if len(h.Entries) == 0 {
					return fail(i, "marker without preceding line")
				}
				h.Entries[len(h.Entries)-1].Line.End = ""
				i++
				continue
			}
			if t == "" {
				return fail(i, "empty line inside hunk")
			}
			e := hunk.Entry{Line: lines.Line{Text: t[1:], End: ls[i].End}}
			switch t[0] {
			case ' ':
				e.Kind, needOld, needNew = edit.Equal, needOld-1, needNew-1
			case '-':
				e.Kind, needOld = edit.Del, needOld-1
			case '+':
				e.Kind, needNew = edit.Ins, needNew-1
			default:
				return fail(i, "bad line prefix")
			}
			if needOld < 0 || needNew < 0 {
				return fail(i, "more lines than declared")
			}
			h.Entries = append(h.Entries, e)
			i++
		}
		if i < len(ls) && ls[i].Text == marker {
			h.Entries[len(h.Entries)-1].Line.End = ""
			i++
		}
		p.Hunks = append(p.Hunks, h)
		if lim.MaxHunks > 0 && len(p.Hunks) > lim.MaxHunks {
			return fail(i, "too many hunks")
		}
	}
	return p, nil
}

func parseHeader(s string) (hunk.Hunk, error) {
	var h hunk.Hunk
	errBad := errors.New("malformed hunk header")
	if !strings.HasPrefix(s, "@@ -") || !strings.HasSuffix(s, " @@") {
		return h, errBad
	}
	parts := strings.Split(s[4:len(s)-3], " ")
	if len(parts) != 2 || !strings.HasPrefix(parts[1], "+") {
		return h, errBad
	}
	var ok bool
	if h.OldStart, h.OldCount, ok = parseSpan(parts[0]); !ok {
		return h, errBad
	}
	if h.NewStart, h.NewCount, ok = parseSpan(parts[1][1:]); !ok {
		return h, errBad
	}
	return h, nil
}

func parseSpan(s string) (start, count int, ok bool) {
	count = 1
	if i := strings.IndexByte(s, ','); i >= 0 {
		c, err := strconv.Atoi(s[i+1:])
		if err != nil {
			return 0, 0, false
		}
		count, s = c, s[:i]
	}
	st, err := strconv.Atoi(s)
	if err != nil || st < 0 || count < 0 {
		return 0, 0, false
	}
	return st, count, true
}
