// Package udiff renders and parses unified diff text.
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

// ErrMalformed marks patch text that is not a valid unified diff.
var ErrMalformed = errors.New("udiff: malformed patch")

// Patch is a parsed or generated unified diff for one file pair.
type Patch struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}

// Limits caps patch size; zero fields use defaults.
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// Diff computes the unified diff between a and b with ctx context lines.
func Diff(oldName, newName string, a, b []byte, ctx, maxDist int) (*Patch, error) {
	hs, err := hunk.Diff(lines.Split(a), lines.Split(b), ctx, maxDist)
	if err != nil {
		return nil, err
	}
	return &Patch{OldName: oldName, NewName: newName, Hunks: hs}, nil
}

// rangeStr formats one half of a hunk header: count 0 keeps the index of
// the preceding line, count 1 omits ",1" (see DESIGN.md).
func rangeStr(idx, count int) string {
	start := idx
	if count > 0 {
		start = idx + 1
	}
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Render serializes p into unified diff text.
func Render(p *Patch) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--- %s\n+++ %s\n", p.OldName, p.NewName)
	for _, h := range p.Hunks {
		fmt.Fprintf(&buf, "@@ -%s +%s @@\n",
			rangeStr(h.OldIdx, h.OldCount), rangeStr(h.NewIdx, h.NewCount))
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

var headerRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@$`)

// Parse reads unified diff text. It is strict: declared line counts must
// match the actual hunk body exactly, and every error carries the 1-based
// line number inside the patch text.
func Parse(text []byte, lim *Limits) (*Patch, error) {
	maxBytes, maxHunks := 1<<20, 4096
	if lim != nil {
		if lim.MaxBytes > 0 {
			maxBytes = lim.MaxBytes
		}
		if lim.MaxHunks > 0 {
			maxHunks = lim.MaxHunks
		}
	}
	if len(text) > maxBytes {
		return nil, fmt.Errorf("udiff: %d bytes exceed limit %d: %w", len(text), maxBytes, ErrMalformed)
	}
	ls := lines.Split(text)
	bad := func(n int, msg string) error {
		return fmt.Errorf("line %d: %s: %w", n, msg, ErrMalformed)
	}
	if len(ls) < 1 || !bytes.HasPrefix(ls[0], []byte("--- ")) {
		return nil, bad(1, "missing --- header")
	}
	if len(ls) < 2 || !bytes.HasPrefix(ls[1], []byte("+++ ")) {
		return nil, bad(2, "missing +++ header")
	}
	p := &Patch{OldName: name(ls[0]), NewName: name(ls[1])}
	i := 2
	for i < len(ls) {
		m := headerRe.FindSubmatch(bytes.TrimSuffix(ls[i], []byte("\n")))
		if m == nil {
			return nil, bad(i+1, "expected @@ hunk header")
		}
		i++
		h := hunk.Hunk{OldCount: num(m[2]), NewCount: num(m[4])}
		h.OldIdx = toIdx(atoi(string(m[1])), h.OldCount)
		h.NewIdx = toIdx(atoi(string(m[3])), h.NewCount)
		if h.OldCount == 0 && h.NewCount == 0 {
			return nil, bad(i, "empty hunk")
		}
		needOld, needNew := h.OldCount, h.NewCount
		for needOld > 0 || needNew > 0 {
			if i >= len(ls) {
				return nil, bad(i+1, "unexpected end of patch")
			}
			ln := i + 1
			kind := ls[i][0]
			if kind != ' ' && kind != '-' && kind != '+' {
				return nil, bad(ln, "line must start with ' ', '-' or '+'")
			}
			body := ls[i][1:]
			i++
			if i < len(ls) && ls[i][0] == '\\' {
				body = bytes.TrimSuffix(body, []byte("\n"))
				i++
			}
			switch kind {
			case ' ':
				needOld--
				needNew--
			case '-':
				needOld--
			default:
				needNew--
			}
			if needOld < 0 || needNew < 0 {
				return nil, bad(ln, "more lines than header declares")
			}
			h.Lines = append(h.Lines, hunk.Line{Kind: kind, Text: body})
		}
		p.Hunks = append(p.Hunks, h)
		if len(p.Hunks) > maxHunks {
			return nil, fmt.Errorf("udiff: more than %d hunks: %w", maxHunks, ErrMalformed)
		}
	}
	return p, nil
}

func name(l []byte) string { return string(bytes.TrimSuffix(l[4:], []byte("\n"))) }

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// num maps an optional ",count" capture to its value (default 1).
func num(c []byte) int {
	if len(c) == 0 {
		return 1
	}
	return atoi(string(c))
}

// toIdx converts a displayed start to a 0-based index (see DESIGN.md).
func toIdx(start, count int) int {
	if count == 0 {
		return start
	}
	return start - 1
}
