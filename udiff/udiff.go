// Package udiff renders and parses unified-diff text. Parsing is strict:
// declared hunk counts must match the body exactly.
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

const noNL = `\ No newline at end of file`

// ErrFormat is the class error for every syntactically malformed patch.
var ErrFormat = errors.New("udiff: malformed patch")

// FormatError wraps ErrFormat and reports the 1-based line in the patch text.
type FormatError struct {
	Line   int
	Reason string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("udiff: format error at line %d: %s", e.Line, e.Reason)
}
func (e *FormatError) Unwrap() error { return ErrFormat }

// Line is one hunk body line; Data keeps the file-side terminator, if any.
type Line struct{ Kind edit.Kind; Data []byte }

// Hunk is one rendered/parsed hunk.
type Hunk struct {
	OldStart, OldCount, NewStart, NewCount int
	Lines                                  []Line
}

// Patch is one unified-diff document.
type Patch struct{ OldName, NewName string; Hunks []Hunk }

// Limits caps parsing effort; zero means unlimited.
type Limits struct{ MaxBytes int64; MaxHunks int }

// Diff builds a Patch from two texts with c context lines and edit cap maxD.
func Diff(old, new []byte, oldName, newName string, c, maxD int, ctr *edit.Counter) (Patch, error) {
	ops, err := edit.Diff(lines.Split(old), lines.Split(new), maxD, ctr)
	if err != nil {
		return Patch{}, err
	}
	p := Patch{OldName: oldName, NewName: newName}
	for _, h := range hunk.Group(ops, c) {
		ph := Hunk{OldStart: h.OldStart, OldCount: h.OldCount, NewStart: h.NewStart, NewCount: h.NewCount}
		for _, it := range h.Items {
			ph.Lines = append(ph.Lines, Line{Kind: it.Kind, Data: it.Data})
		}
		p.Hunks = append(p.Hunks, ph)
	}
	return p, nil
}

// Render writes the canonical unified-diff text of p.
func Render(p Patch) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", p.OldName, p.NewName)
	for _, h := range p.Hunks {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", rng(h.OldStart, h.OldCount), rng(h.NewStart, h.NewCount))
		for i, ln := range h.Lines {
			pre := " "
			if ln.Kind == edit.Delete {
				pre = "-"
			} else if ln.Kind == edit.Insert {
				pre = "+"
			}
			b.WriteString(pre)
			b.Write(ln.Data)
			if !bytes.HasSuffix(ln.Data, []byte{'\n'}) {
				b.WriteByte('\n')
				b.WriteString(noNL)
				if i != len(h.Lines)-1 {
					b.WriteByte('\n')
				}
			}
		}
	}
	return b.Bytes()
}

func rng(start, count int) string {
	if count == 1 { return strconv.Itoa(start) }
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Parse strictly parses unified-diff text under the given resource limits.
func Parse(data []byte, lim Limits) (Patch, error) {
	if lim.MaxBytes > 0 && int64(len(data)) > lim.MaxBytes {
		return Patch{}, &FormatError{Line: 1, Reason: "patch exceeds byte limit"}
	}
	raw := lines.Split(data)
	if len(raw) < 2 || !bytes.HasPrefix(raw[0].Data, []byte("--- ")) ||
		!bytes.HasPrefix(raw[1].Data, []byte("+++ ")) {
		return Patch{}, &FormatError{Line: min(len(raw)+1, 2), Reason: "missing file headers"}
	}
	p := Patch{OldName: string(bytes.TrimSpace(raw[0].Data[4:])), NewName: string(bytes.TrimSpace(raw[1].Data[5:]))}
	for i := 2; i < len(raw); {
		h, ni, err := parseHunk(raw, i)
		if err != nil {
			return Patch{}, err
		}
		if lim.MaxHunks > 0 && len(p.Hunks) >= lim.MaxHunks {
			return Patch{}, &FormatError{Line: i + 1, Reason: "hunk count exceeds limit"}
		}
		p.Hunks = append(p.Hunks, h)
		i = ni
	}
	return p, nil
}

func parseHunk(raw []lines.Line, i int) (Hunk, int, error) {
	var h Hunk
	os, oc, ns, nc, err := parseHeader(raw[i].Data)
	if err != nil {
		return h, i, &FormatError{Line: i + 1, Reason: err.Error()}
	}
	h.OldStart, h.OldCount, h.NewStart, h.NewCount = os, oc, ns, nc
	ri, gotO, gotN := i+1, 0, 0
	for gotO < oc || gotN < nc {
		if ri >= len(raw) {
			return h, ri, &FormatError{Line: ri + 1, Reason: "truncated hunk body"}
		}
		s := raw[ri].Data
		if len(s) == 0 {
			return h, ri, &FormatError{Line: ri + 1, Reason: "line missing diff prefix"}
		}
		switch pre := s[0]; pre {
		case ' ', '-', '+':
			k := edit.Equal
			if pre == '-' {
				k, gotO = edit.Delete, gotO+1
			} else if pre == '+' {
				k, gotN = edit.Insert, gotN+1
			} else {
				gotO++
				gotN++
			}
			data := s[1:]
			if ri+1 < len(raw) && string(raw[ri+1].Data) == noNL {
				data = bytes.TrimSuffix(data, []byte{'\n'})
				ri++
			}
			h.Lines = append(h.Lines, Line{Kind: k, Data: data})
			ri++
		default:
			return h, ri, &FormatError{Line: ri + 1, Reason: "invalid diff prefix"}
		}
	}
	return h, ri, nil
}

func parseHeader(s []byte) (int, int, int, int, error) {
	if !bytes.HasPrefix(s, []byte("@@ ")) {
		return 0, 0, 0, 0, errors.New("missing hunk header")
	}
	end := bytes.Index(s[3:], []byte(" @@"))
	if end < 0 {
		return 0, 0, 0, 0, errors.New("malformed hunk header")
	}
	var o, n string
	if _, err := fmt.Sscanf(string(s[3:3+end]), "-%s +%s", &o, &n); err != nil || o == "" || n == "" {
		return 0, 0, 0, 0, errors.New("malformed hunk ranges")
	}
	os, oc, err := parseRange(o)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	ns, nc, err := parseRange(n)
	return os, oc, ns, nc, err
}

func parseRange(s string) (start, count int, err error) {
	count = 1
	if i := strings.IndexByte(s, ','); i >= 0 {
		if count, err = strconv.Atoi(s[i+1:]); err != nil {
			return 0, 0, err
		}
		s = s[:i]
	}
	start, err = strconv.Atoi(s)
	return start, count, err
}
