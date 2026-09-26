// Package udiff renders hunks as unified diff text and parses such
// text back, strictly validating hunk structure and declared counts.
package udiff

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

// ErrFormat marks malformed patch text; errors carry the line number.
var ErrFormat = errors.New("udiff: malformed patch")

// ErrLimit marks patches exceeding configured resource limits.
var ErrLimit = errors.New("udiff: patch exceeds limits")

// Limits caps patch resources; zero fields fall back to defaults.
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// Patch is one parsed single-file patch.
type Patch struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}

// Diff computes the hunks between a and b with c context lines.
func Diff(a, b []byte, c int) []hunk.Hunk {
	ops, err := edit.DiffBytes(a, b, -1)
	if err != nil {
		panic(err) // unlimited distance: unreachable
	}
	return hunk.Group(ops, c)
}

// Render formats hunks as unified diff text with the given file names.
func Render(hs []hunk.Hunk, oldName, newName string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--- %s\n+++ %s\n", oldName, newName)
	for _, h := range hs {
		buf.WriteString("@@ -")
		writeRange(&buf, h.OldStart, h.OldCount)
		buf.WriteString(" +")
		writeRange(&buf, h.NewStart, h.NewCount)
		buf.WriteString(" @@\n")
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

func writeRange(buf *bytes.Buffer, start, count int) {
	fmt.Fprintf(buf, "%d", start)
	if count != 1 {
		fmt.Fprintf(buf, ",%d", count)
	}
}

var hunkRe = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

// Parse reads unified diff text into a Patch, checking that every
// hunk's declared counts match its actual lines exactly.
func Parse(text []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes <= 0 {
		lim.MaxBytes = 1 << 20
	}
	if lim.MaxHunks <= 0 {
		lim.MaxHunks = 4096
	}
	if len(text) > lim.MaxBytes {
		return nil, ErrLimit
	}
	ls := lines.Split(text)
	bad := func(i int, msg string) error {
		return fmt.Errorf("%w: line %d: %s", ErrFormat, i, msg)
	}
	if len(ls) < 2 || !bytes.HasPrefix(ls[0], []byte("--- ")) {
		return nil, bad(1, "missing --- header")
	}
	if !bytes.HasPrefix(ls[1], []byte("+++ ")) {
		return nil, bad(2, "missing +++ header")
	}
	p := &Patch{
		OldName: strings.TrimRight(string(ls[0][4:]), "\r\n"),
		NewName: strings.TrimRight(string(ls[1][4:]), "\r\n"),
	}
	i := 2
	for i < len(ls) {
		m := hunkRe.FindStringSubmatch(strings.TrimRight(string(ls[i]), "\r\n"))
		if m == nil {
			return nil, bad(i+1, "expected @@ hunk header")
		}
		i++
		h := hunk.Hunk{
			OldStart: atoi(m[1]), OldCount: cnt(m[2]),
			NewStart: atoi(m[3]), NewCount: cnt(m[4]),
		}
		for needOld, needNew := h.OldCount, h.NewCount; needOld > 0 || needNew > 0; {
			if i >= len(ls) {
				return nil, bad(i+1, "hunk truncated")
			}
			k := ls[i][0]
			if k != ' ' && k != '-' && k != '+' {
				return nil, bad(i+1, "bad hunk line prefix")
			}
			text := ls[i][1:]
			i++
			if i < len(ls) && ls[i][0] == '\\' {
				text = bytes.TrimSuffix(text, []byte("\n"))
				i++
			}
			if k != '+' {
				needOld--
			}
			if k != '-' {
				needNew--
			}
			if needOld < 0 || needNew < 0 {
				return nil, bad(i, "hunk has more lines than declared")
			}
			h.Lines = append(h.Lines, hunk.Line{Kind: k, Text: text})
		}
		p.Hunks = append(p.Hunks, h)
		if len(p.Hunks) > lim.MaxHunks {
			return nil, ErrLimit
		}
	}
	return p, nil
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func cnt(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}
