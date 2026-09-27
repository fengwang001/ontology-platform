// Package udiff renders and strictly parses unified-diff text.
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

// Kind of a parsed body line.
const (
	Context byte = ' '
	Old     byte = '-'
	New     byte = '+'
)

const noNLMarker = "\\ No newline at end of file"

type Line struct{ Kind byte; Text string; CR bool }
type Hunk struct {
	OldStart, OldCount, NewStart, NewCount int
	Lines                                  []Line
	OldNoNL, NewNoNL                       []int
}
type Patch struct{ Hunks []Hunk }

var ErrFormat = errors.New("udiff: malformed patch")

type FormatError struct{ Line int }

func (e *FormatError) Error() string { return fmt.Sprintf("%s at patch line %d", ErrFormat, e.Line) }
func (e *FormatError) Unwrap() error { return ErrFormat }

func Format(err error) bool { return errors.Is(err, ErrFormat) }

type Limits struct{ MaxBytes, MaxHunks int }

func Diff(a, b []byte, c int) ([]byte, error) {
	al, bl := lines.Split(a), lines.Split(b)
	ops, err := new(edit.Differ).Diff(al, bl)
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	sb.WriteString("--- a\n+++ b\n")
	for _, h := range hunk.Build(ops, al, bl, c) {
		fmt.Fprintf(&sb, "@@ -%s +%s @@\n", rng(h.OldStart, h.OldCount), rng(h.NewStart, h.NewCount))
		for _, it := range h.Items {
			sb.WriteByte(byte(it.Op.Kind))
			sb.Write(it.Op.Line.Text)
			nl := "\n"
			if len(it.Op.Line.NL) == 2 {
				nl = "\r\n"
			}
			if len(it.Op.Line.NL) == 0 && (it.OldNoNL || it.NewNoNL) {
				nl = "\n" + noNLMarker + "\n"
			}
			sb.WriteString(nl)
		}
	}
	return []byte(sb.String()), nil
}

func rng(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

func Parse(text []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(text) > lim.MaxBytes {
		return nil, &FormatError{1}
	}
	raw := strings.Split(string(text), "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	if len(raw) < 2 || raw[0] != "--- a" || raw[1] != "+++ b" {
		return nil, &FormatError{1}
	}
	p := &Patch{}
	for i := 2; i < len(raw); {
		h, ni, err := parseHunk(raw, i)
		if err != nil {
			return nil, err
		}
		if lim.MaxHunks > 0 && len(p.Hunks) >= lim.MaxHunks {
			return nil, &FormatError{i + 1}
		}
		p.Hunks, i = append(p.Hunks, h), ni
	}
	return p, nil
}

func parseHunk(raw []string, i int) (Hunk, int, error) {
	var h Hunk
	headLine := i + 1
	os, oc, ns, nc, ok := parseHead(raw[i])
	if !ok {
		return h, i, &FormatError{headLine}
	}
	h.OldStart, h.OldCount, h.NewStart, h.NewCount = os, oc, ns, nc
	i++
	gotO, gotN := 0, 0
	for i < len(raw) && (gotO < oc || gotN < nc) {
		r := raw[i]
		if len(r) == 0 || (r[0] != ' ' && r[0] != '-' && r[0] != '+') {
			return h, i, &FormatError{i + 1}
		}
		body := r[1:]
		cr := strings.HasSuffix(body, "\r")
		if cr {
			body = body[:len(body)-1]
		}
		idx := len(h.Lines)
		h.Lines = append(h.Lines, Line{r[0], body, cr})
		if r[0] != '+' {
			gotO++
		}
		if r[0] != '-' {
			gotN++
		}
		i++
		if i < len(raw) && raw[i] == noNLMarker {
			if r[0] != '+' {
				h.OldNoNL = append(h.OldNoNL, idx)
			}
			if r[0] != '-' {
				h.NewNoNL = append(h.NewNoNL, idx)
			}
			i++
		}
	}
	if gotO != oc || gotN != nc {
		return h, i, &FormatError{headLine}
	}
	return h, i, nil
}

func parseHead(s string) (os, oc, ns, nc int, ok bool) {
	if !strings.HasPrefix(s, "@@ ") || !strings.HasSuffix(s, " @@") {
		return
	}
	parts := strings.SplitN(s[3:len(s)-3], " ", 2)
	if len(parts) != 2 || len(parts[0]) < 2 || len(parts[1]) < 2 {
		return
	}
	os, oc, ok1 := oneRange(parts[0])
	ns, nc, ok2 := oneRange(parts[1])
	return os, oc, ns, nc, ok1 && ok2
}

func oneRange(s string) (start, count int, ok bool) {
	if s[0] != '-' && s[0] != '+' {
		return 0, 0, false
	}
	count = 1
	body := s[1:]
	if i := strings.IndexByte(body, ','); i >= 0 {
		s1, e1 := atoi(body[:i])
		s2, e2 := atoi(body[i+1:])
		return s1, s2, e1 && e2
	}
	start, ok = atoi(body)
	return
}

func atoi(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil
}
