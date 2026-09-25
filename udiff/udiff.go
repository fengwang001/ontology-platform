// Package udiff renders and strictly parses unified diff text.
package udiff

import (
	"bytes"
	"errors"
	"fmt"
	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"strconv"
	"strings"
)

// ErrFormat marks malformed patch text (wrapped with the 1-based line
// number); ErrLimit marks patches exceeding configured size limits.
var (
	ErrFormat = errors.New("udiff: malformed patch")
	ErrLimit  = errors.New("udiff: patch exceeds configured limits")
)

type Limits struct{ MaxBytes, MaxHunks int } // zero fields mean unlimited

// Patch is a generated or parsed unified diff.
type Patch struct{ Hunks []hunk.Hunk }

// Diff groups the shortest script from a to b into hunks with ctx context lines.
func Diff(a, b []lines.Line, ctx int) *Patch {
	ops, err := edit.Diff(a, b, -1)
	if err != nil { // unlimited distance never fails
		panic(err)
	}
	return &Patch{Hunks: hunk.Group(ops, a, b, ctx)}
}

// Render formats p as unified diff text; no hunks yields empty output.
func Render(p *Patch) []byte {
	if len(p.Hunks) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteString("--- a\n+++ b\n")
	for _, h := range p.Hunks {
		fmt.Fprintf(&buf, "@@ -%s +%s @@\n", rng(h.OldStart, h.OldCount), rng(h.NewStart, h.NewCount))
		for _, l := range h.Lines {
			buf.WriteByte(l.Kind)
			buf.Write(l.Text)
			if !lines.HasEOL(l.Text) {
				buf.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return buf.Bytes()
}

func rng(start, count int) string {
	if count == 0 {
		return strconv.Itoa(start) + ",0"
	}
	if count == 1 {
		return strconv.Itoa(start + 1)
	}
	return strconv.Itoa(start+1) + "," + strconv.Itoa(count)
}

func bad(line int, msg string) error {
	return fmt.Errorf("%w: line %d: %s", ErrFormat, line, msg)
}

func parseRange(s string) (start, count int, ok bool) {
	parts := strings.Split(s, ",")
	start, e1 := strconv.Atoi(parts[0])
	count, e2 := 1, error(nil)
	if len(parts) == 2 {
		count, e2 = strconv.Atoi(parts[1])
	}
	if len(parts) > 2 || e1 != nil || e2 != nil || start < 0 || count < 0 {
		return 0, 0, false
	}
	return start, count, true
}

// Parse reads unified diff text strictly: declared hunk counts must match
// the actual lines exactly; errors carry the 1-based patch line number.
func Parse(data []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, ErrLimit
	}
	p := &Patch{}
	if len(data) == 0 {
		return p, nil
	}
	raw := bytes.Split(data, []byte("\n"))
	if len(raw[len(raw)-1]) == 0 {
		raw = raw[:len(raw)-1]
	}
	if len(raw) < 2 || !strings.HasPrefix(string(raw[0]), "--- ") || !strings.HasPrefix(string(raw[1]), "+++ ") {
		return nil, bad(1, "missing ---/+++ header")
	}
	for i := 2; i < len(raw); {
		f := strings.Fields(strings.TrimSuffix(string(raw[i]), " @@"))
		if len(f) != 3 || f[0] != "@@" || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
			return nil, bad(i+1, "bad hunk header")
		}
		os, oc, ok1 := parseRange(f[1][1:])
		ns, nc, ok2 := parseRange(f[2][1:])
		if !ok1 || !ok2 {
			return nil, bad(i+1, "bad hunk header")
		}
		var h hunk.Hunk
		h.OldStart, h.OldCount, h.NewStart, h.NewCount = os-1, oc, ns-1, nc
		if oc == 0 {
			h.OldStart = os
		}
		if nc == 0 {
			h.NewStart = ns
		}
		i++
		for oc+nc > 0 {
			if i >= len(raw) {
				return nil, bad(i+1, "truncated hunk body")
			}
			l := raw[i]
			if len(l) == 0 || (l[0] != ' ' && l[0] != '-' && l[0] != '+') {
				return nil, bad(i+1, "bad content line")
			}
			k := l[0]
			if (k != '+' && oc == 0) || (k != '-' && nc == 0) {
				return nil, bad(i+1, "more lines than declared")
			}
			if k != '+' {
				oc--
			}
			if k != '-' {
				nc--
			}
			text := append(append([]byte{}, l[1:]...), '\n')
			if i+1 < len(raw) && string(raw[i+1]) == `\ No newline at end of file` {
				text, i = text[:len(text)-1], i+1
			}
			h.Lines = append(h.Lines, hunk.Line{Kind: k, Text: text})
			i++
		}
		if lim.MaxHunks > 0 && len(p.Hunks) >= lim.MaxHunks {
			return nil, ErrLimit
		}
		p.Hunks = append(p.Hunks, h)
	}
	return p, nil
}
