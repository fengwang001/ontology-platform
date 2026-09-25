// Package udiff renders and parses unified diff text. Parse is strict:
// hunk body line counts must match the header exactly.
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

// ErrFormat marks malformed patch text; ErrLimit marks exceeded size caps.
var (
	ErrFormat = errors.New("udiff: format error")
	ErrLimit  = errors.New("udiff: resource limit exceeded")
)

// ParseError carries the 1-based line number of a format error.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return fmt.Sprintf("udiff: line %d: %s", e.Line, e.Msg) }
func (e *ParseError) Unwrap() error { return ErrFormat }

// Patch is a parsed or generated unified diff.
type Patch struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}

// Diff computes hunks for a -> b with ctx context lines. maxDist >= 0 caps
// the edit distance (edit.ErrTooLarge when exceeded).
func Diff(a, b []byte, ctx, maxDist int) (*Patch, error) {
	la, lb := lines.Split(a), lines.Split(b)
	ops, err := edit.Diff(la, lb, maxDist)
	if err != nil {
		return nil, err
	}
	return &Patch{"a", "b", hunk.Group(ops, la, lb, ctx)}, nil
}

// Render formats p as unified diff text. Empty patches render empty.
func Render(p *Patch) []byte {
	if len(p.Hunks) == 0 {
		return nil
	}
	var out bytes.Buffer
	out.WriteString("--- " + p.OldName + "\n")
	out.WriteString("+++ " + p.NewName + "\n")
	for _, h := range p.Hunks {
		fmt.Fprintf(&out, "@@ -%s +%s @@\n", rng(h.OldStart, h.OldCount), rng(h.NewStart, h.NewCount))
		for _, l := range h.Lines {
			out.WriteByte(l.Kind)
			out.Write(l.Text)
			if !bytes.HasSuffix(l.Text, []byte("\n")) {
				out.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return out.Bytes()
}

func rng(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Parse decodes unified diff text. maxBytes/maxHunks > 0 impose limits.
func Parse(data []byte, maxBytes, maxHunks int) (*Patch, error) {
	if maxBytes > 0 && len(data) > maxBytes {
		return nil, fmt.Errorf("%w: patch too large", ErrLimit)
	}
	ls := lines.Split(data)
	p := &Patch{}
	for i := 0; i < len(ls); i++ {
		line := ls[i]
		if bytes.HasPrefix(line, []byte("--- ")) {
			if i+1 >= len(ls) || !bytes.HasPrefix(ls[i+1], []byte("+++ ")) {
				return nil, &ParseError{i + 1, "expected +++ after ---"}
			}
			p.OldName = string(bytes.TrimRight(ls[i][4:], "\n"))
			p.NewName = string(bytes.TrimRight(ls[i+1][4:], "\n"))
			i++
			continue
		}
		h, err := parseHeader(line)
		if err != nil {
			return nil, &ParseError{i + 1, err.Error()}
		}
		i++
		needOld, needNew := h.OldCount, h.NewCount
		for needOld > 0 || needNew > 0 {
			if i >= len(ls) {
				return nil, &ParseError{i + 1, "hunk body truncated"}
			}
			c := ls[i][0]
			if c == '\\' {
				if !stripNL(h.Lines) {
					return nil, &ParseError{i + 1, "stray \\ line"}
				}
				i++
				continue
			}
			switch c {
			case ' ':
				if needOld == 0 || needNew == 0 {
					return nil, &ParseError{i + 1, "hunk line count mismatch"}
				}
				needOld--
				needNew--
			case '-':
				if needOld == 0 {
					return nil, &ParseError{i + 1, "hunk line count mismatch"}
				}
				needOld--
			case '+':
				if needNew == 0 {
					return nil, &ParseError{i + 1, "hunk line count mismatch"}
				}
				needNew--
			default:
				return nil, &ParseError{i + 1, "invalid line prefix"}
			}
			h.Lines = append(h.Lines, hunk.Line{Kind: c, Text: ls[i][1:]})
			i++
		}
		if i < len(ls) && ls[i][0] == '\\' {
			if !stripNL(h.Lines) {
				return nil, &ParseError{i + 1, "stray \\ line"}
			}
			i++
		}
		p.Hunks = append(p.Hunks, h)
		if maxHunks > 0 && len(p.Hunks) > maxHunks {
			return nil, fmt.Errorf("%w: too many hunks", ErrLimit)
		}
		i--
	}
	return p, nil
}

func stripNL(ls []hunk.Line) bool {
	if len(ls) == 0 || !bytes.HasSuffix(ls[len(ls)-1].Text, []byte("\n")) {
		return false
	}
	ls[len(ls)-1].Text = ls[len(ls)-1].Text[:len(ls[len(ls)-1].Text)-1]
	return true
}

func parseHeader(line []byte) (hunk.Hunk, error) {
	s := strings.TrimSuffix(string(line), "\n")
	if !strings.HasPrefix(s, "@@ -") {
		return hunk.Hunk{}, errors.New("expected @@ hunk header")
	}
	s = s[len("@@ -"):]
	i := strings.Index(s, " +")
	if i < 0 {
		return hunk.Hunk{}, errors.New("missing + range")
	}
	oldPart, rest := s[:i], s[i+2:]
	i = strings.Index(rest, " @@")
	if i < 0 {
		return hunk.Hunk{}, errors.New("missing closing @@")
	}
	newPart := rest[:i]
	os, oc, err := rngParse(oldPart)
	if err != nil {
		return hunk.Hunk{}, err
	}
	ns, nc, err := rngParse(newPart)
	if err != nil {
		return hunk.Hunk{}, err
	}
	return hunk.Hunk{OldStart: os, OldCount: oc, NewStart: ns, NewCount: nc}, nil
}

func rngParse(s string) (int, int, error) {
	parts := strings.Split(s, ",")
	if len(parts) > 2 {
		return 0, 0, errors.New("invalid range")
	}
	start, err := strconv.Atoi(parts[0])
	if err != nil || start < 0 {
		return 0, 0, errors.New("invalid range start")
	}
	count := 1
	if len(parts) == 2 {
		c, err := strconv.Atoi(parts[1])
		if err != nil || c < 0 {
			return 0, 0, errors.New("invalid range count")
		}
		count = c
	}
	if count > 0 && start < 1 {
		return 0, 0, errors.New("start must be >= 1 when count > 0")
	}
	return start, count, nil
}
