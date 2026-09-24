// Package udiff renders hunks as unified diff text and parses it back.
package udiff

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"ontology/hunk"
	"ontology/lines"
)

// Line is one hunk body line; Text keeps the line terminator unless the
// line was marked "\ No newline at end of file".
type Line struct {
	Kind byte
	Text []byte
}

// Hunk is a parsed hunk with its header numbers.
type Hunk struct {
	OldStart, OldCount, NewStart, NewCount int
	Lines                                  []Line
}

// Patch is a parsed unified diff.
type Patch struct {
	Hunks []Hunk
}

// ErrFormat reports malformed patch text (errors carry a line number);
// ErrLimit reports a patch beyond the configured size limits.
var (
	ErrFormat = errors.New("udiff: malformed patch")
	ErrLimit  = errors.New("udiff: patch exceeds limit")
)

var headRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@\n$`)

// Diff renders the unified diff between a and b with ctx context lines.
func Diff(a, b []byte, ctx, limit int) ([]byte, error) {
	hs, err := hunk.Diff(a, b, ctx, limit)
	if err != nil {
		return nil, err
	}
	return Render(hs, lines.Split(a), lines.Split(b)), nil
}

// Render formats hunks as unified diff text over the line splits a and b.
func Render(hs []hunk.Hunk, a, b [][]byte) []byte {
	if len(hs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteString("--- a\n+++ b\n")
	for _, h := range hs {
		fmt.Fprintf(&buf, "@@ -%s +%s @@\n", rng(h.OldStart, h.OldCount), rng(h.NewStart, h.NewCount))
		for _, op := range h.Ops {
			ln := a[op.Old]
			if op.Kind == '+' {
				ln = b[op.New]
			}
			buf.WriteByte(op.Kind)
			buf.Write(ln)
			if !bytes.HasSuffix(ln, []byte("\n")) {
				buf.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return buf.Bytes()
}

func rng(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Parse strictly parses unified diff text. Limits <= 0 mean unbounded.
func Parse(data []byte, maxBytes, maxHunks int) (*Patch, error) {
	if maxBytes > 0 && len(data) > maxBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrLimit, len(data))
	}
	p := &Patch{}
	if len(data) == 0 {
		return p, nil
	}
	raw := lines.Split(data)
	bad := func(line int, msg string) error {
		return fmt.Errorf("%w: line %d: %s", ErrFormat, line, msg)
	}
	if !bytes.HasPrefix(raw[0], []byte("--- ")) {
		return nil, bad(1, "missing --- header")
	}
	if len(raw) < 2 || !bytes.HasPrefix(raw[1], []byte("+++ ")) {
		return nil, bad(2, "missing +++ header")
	}
	i := 2
	for i < len(raw) {
		m := headRe.FindSubmatch(raw[i])
		if m == nil {
			return nil, bad(i+1, "malformed hunk header")
		}
		h := Hunk{OldStart: num(m[1], 0), OldCount: num(m[2], 1), NewStart: num(m[3], 0), NewCount: num(m[4], 1)}
		i++
		needOld, needNew := h.OldCount, h.NewCount
		for needOld > 0 || needNew > 0 {
			if i >= len(raw) {
				return nil, bad(i+1, "hunk body shorter than header declares")
			}
			l := raw[i]
			kind := l[0]
			if kind != ' ' && kind != '-' && kind != '+' {
				return nil, bad(i+1, "bad line prefix")
			}
			if kind != '+' {
				needOld--
			}
			if kind != '-' {
				needNew--
			}
			if needOld < 0 || needNew < 0 {
				return nil, bad(i+1, "hunk body longer than header declares")
			}
			text := l[1:]
			if i+1 < len(raw) && raw[i+1][0] == '\\' {
				text = bytes.TrimSuffix(text, []byte("\n"))
				i++
			} else if !bytes.HasSuffix(text, []byte("\n")) {
				return nil, bad(i+1, "truncated line")
			}
			h.Lines = append(h.Lines, Line{kind, text})
			i++
		}
		p.Hunks = append(p.Hunks, h)
		if maxHunks > 0 && len(p.Hunks) > maxHunks {
			return nil, fmt.Errorf("%w: %d hunks", ErrLimit, len(p.Hunks))
		}
	}
	return p, nil
}

func num(b []byte, def int) int {
	if len(b) == 0 {
		return def
	}
	n, _ := strconv.Atoi(string(b))
	return n
}
