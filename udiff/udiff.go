// Package udiff renders and parses unified diff text.
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

// ErrFormat is the "格式错误" class; errors carry the patch line number.
var ErrFormat = errors.New("udiff: malformed patch")

// Patch is a parsed single-file diff; Limits caps accepted patch size.
type Patch struct{ Hunks []hunk.Hunk }
type Limits struct{ MaxBytes, MaxHunks int }

const noNewline = "\\ No newline at end of file\n"

// Render produces the unified diff of a and b with ctx context lines,
// using fixed header names "a"/"b"; identical inputs render to nil.
func Render(a, b []byte, ctx int) []byte {
	ops, err := edit.Diff(lines.Split(a), lines.Split(b), 0)
	if err != nil {
		panic(err) // unlimited cap: unreachable
	}
	hs := hunk.Group(ops, ctx)
	if len(hs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteString("--- a\n+++ b\n")
	for _, h := range hs {
		fmt.Fprintf(&buf, "@@ -%s +%s @@\n", rng(h.AStart, h.ACount), rng(h.BStart, h.BCount))
		for _, op := range h.Ops {
			buf.WriteByte(op.Kind)
			buf.Write(op.Line)
			if !bytes.HasSuffix(op.Line, []byte("\n")) {
				buf.WriteString("\n" + noNewline)
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

// Parse parses p strictly: declared hunk counts must match the body exactly.
func Parse(p []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(p) > lim.MaxBytes {
		return nil, fmt.Errorf("%w: size %d over limit %d", ErrFormat, len(p), lim.MaxBytes)
	}
	ls := lines.Split(p)
	if len(ls) == 0 {
		return &Patch{}, nil
	}
	if len(ls) < 2 || !bytes.HasPrefix(ls[0], []byte("--- ")) ||
		!bytes.HasPrefix(ls[1], []byte("+++ ")) {
		return nil, ferr(1, "missing file headers")
	}
	out := &Patch{}
	for pos := 2; pos < len(ls); {
		h, next, err := parseHunk(ls, pos)
		if err != nil {
			return nil, err
		}
		out.Hunks = append(out.Hunks, *h)
		if lim.MaxHunks > 0 && len(out.Hunks) > lim.MaxHunks {
			return nil, ferr(pos+1, "hunk count over limit")
		}
		pos = next
	}
	return out, nil
}

func parseHunk(ls [][]byte, pos int) (*hunk.Hunk, int, error) {
	s := string(ls[pos])
	if !strings.HasPrefix(s, "@@ -") || !strings.HasSuffix(s, " @@\n") {
		return nil, pos, ferr(pos+1, "bad hunk header")
	}
	mid := strings.Index(s, " +")
	if mid < 0 {
		return nil, pos, ferr(pos+1, "bad hunk header")
	}
	as, bs := s[4:mid], s[mid+2:len(s)-4]
	var h hunk.Hunk
	var err error
	if h.AStart, h.ACount, err = parseRange(as); err != nil {
		return nil, pos, ferr(pos+1, "bad old range")
	}
	if h.BStart, h.BCount, err = parseRange(bs); err != nil {
		return nil, pos, ferr(pos+1, "bad new range")
	}
	needA, needB := h.ACount, h.BCount
	for needA > 0 || needB > 0 {
		pos++
		if pos >= len(ls) {
			return nil, pos, ferr(pos+1, "hunk body truncated")
		}
		l := ls[pos]
		if len(l) < 2 {
			return nil, pos, ferr(pos+1, "bad line prefix")
		}
		c, content := l[0], l[1:]
		if c != ' ' && c != '-' && c != '+' {
			return nil, pos, ferr(pos+1, "bad line prefix")
		}
		if pos+1 < len(ls) && ls[pos+1][0] == '\\' {
			if !bytes.Equal(ls[pos+1], []byte(noNewline)) {
				return nil, pos + 1, ferr(pos+2, "bad no-newline marker")
			}
			content = content[:len(content)-1]
			pos++
		}
		h.Ops = append(h.Ops, edit.Op{Kind: c, Line: content})
		if c != '+' {
			needA--
		}
		if c != '-' {
			needB--
		}
	}
	if needA != 0 || needB != 0 {
		return nil, pos, ferr(pos+1, "hunk line counts do not match header")
	}
	return &h, pos + 1, nil
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

func ferr(line int, msg string) error {
	return fmt.Errorf("%w: line %d: %s", ErrFormat, line, msg)
}
