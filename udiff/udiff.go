package udiff

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

const noNL = `\ No newline at end of file`

// FormatError names the patch-text line number where parsing failed.
type FormatError struct {
	Line    int // 1-based line number within the patch text
	Message string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("udiff: malformed patch at line %d: %s", e.Line, e.Message)
}

// Patch is a parsed unified diff for one file pair.
type Patch struct {
	OldName string
	NewName string
	Hunks   []hunk.Hunk
	OldNoNL bool
	NewNoNL bool
}

// Render builds unified-diff text from a grouped script.
// oldNoNL/newNoNL state whether each original file lacked a final newline.
func Render(oldName, newName string, hs []hunk.Hunk, oldNoNL, newNoNL bool) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "--- %s\n", oldName)
	fmt.Fprintf(&b, "+++ %s\n", newName)
	for _, h := range hs {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", spec(h.OldStart, h.OldCount), spec(h.NewStart, h.NewCount))
		writeOps(&b, h.Ops, oldNoNL, newNoNL)
	}
	return b.Bytes()
}

func spec(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

func writeOps(b *bytes.Buffer, ops []edit.Op, oldNoNL, newNoNL bool) {
	oldTotal, newTotal := sideTotals(ops)
	oldIdx, newIdx := 0, 0
	for _, op := range ops {
		mark := byte(' ')
		l := op.Line
		switch op.Kind {
		case edit.Delete:
			mark = '-'
			oldIdx++
		case edit.Insert:
			mark = '+'
			newIdx++
		default:
			oldIdx++
			newIdx++
		}
		b.WriteByte(mark)
		b.Write(l.Data)
		b.Write(l.End)
		emit := lines.NoNewline(l) &&
			((op.Kind == edit.Delete && oldNoNL && oldIdx == oldTotal) ||
				(op.Kind == edit.Insert && newNoNL && newIdx == newTotal) ||
				(op.Kind == edit.Equal && oldNoNL && newNoNL && oldIdx == oldTotal))
		if emit {
			b.WriteString(noNL + "\n")
		}
	}
}

func sideTotals(ops []edit.Op) (int, int) {
	o, n := 0, 0
	for _, op := range ops {
		switch op.Kind {
		case edit.Equal:
			o++
			n++
		case edit.Delete:
			o++
		case edit.Insert:
			n++
		}
	}
	return o, n
}

// Parse decodes unified-diff text strictly (requirement: hunk header
// counts must match the body exactly).
func Parse(data []byte) (*Patch, error) {
	return parseLimited(data, 0, 0)
}

// ParseLimited additionally rejects oversized patches.
func ParseLimited(data []byte, maxBytes, maxHunks int) (*Patch, error) {
	return parseLimited(data, maxBytes, maxHunks)
}

func parseLimited(data []byte, maxBytes, maxHunks int) (*Patch, error) {
	if maxBytes > 0 && len(data) > maxBytes {
		return nil, &FormatError{Line: 1, Message: "patch exceeds byte limit"}
	}
	ls := splitTextLines(data)
	if len(ls) < 2 || !bytes.HasPrefix(ls[0], []byte("--- ")) || !bytes.HasPrefix(ls[1], []byte("+++ ")) {
		return nil, &FormatError{Line: 1, Message: "expected --- and +++ headers"}
	}
	p := &Patch{
		OldName: string(bytes.TrimPrefix(ls[0], []byte("--- "))),
		NewName: string(bytes.TrimPrefix(ls[1], []byte("+++ "))),
	}
	ln := 2
	for ln < len(ls) {
		h, consumed, err := parseHunk(ls, ln)
		if err != nil {
			return nil, err
		}
		p.Hunks = append(p.Hunks, h)
		if maxHunks > 0 && len(p.Hunks) > maxHunks {
			return nil, &FormatError{Line: ln + 1, Message: "patch exceeds hunk limit"}
		}
		ln += consumed
	}
	p.OldNoNL = anyMarker(ls, edit.Delete) || anyMarker(ls, edit.Equal)
	p.NewNoNL = anyMarker(ls, edit.Insert) || anyMarker(ls, edit.Equal)
	return p, nil
}

func anyMarker(ls [][]byte, kind edit.Kind) bool {
	mark := byte(' ')
	if kind == edit.Delete {
		mark = '-'
	}
	if kind == edit.Insert {
		mark = '+'
	}
	for i, l := range ls {
		if i+1 < len(ls) && bytes.HasPrefix(l, []byte{mark}) && !bytes.HasSuffix(l, []byte("\n")) &&
			bytes.Equal(bytes.TrimRight(ls[i+1], "\n"), []byte(noNL)) {
			return true
		}
	}
	return false
}

func parseHunk(ls [][]byte, start int) (hunk.Hunk, int, error) {
	var h hunk.Hunk
	head := ls[start]
	os_, oc, ns, nc, ok := parseHeader(head)
	if !ok {
		return h, 0, &FormatError{Line: start + 1, Message: "bad hunk header"}
	}
	h.OldStart, h.OldCount, h.NewStart, h.NewCount = os_, oc, ns, nc
	ln := start + 1
	gotO, gotN := 0, 0
	for gotO < oc || gotN < nc {
		if ln >= len(ls) {
			return h, 0, &FormatError{Line: ln + 1, Message: "hunk body truncated"}
		}
		row := ls[ln]
		if len(row) == 0 {
			return h, 0, &FormatError{Line: ln + 1, Message: "hunk line missing leading marker"}
		}
		// A context line for an empty input file is a single space,
		// which must be accepted, not rejected.
		switch row[0] {
		case '-':
			gotO++
		case '+':
			gotN++
		case ' ':
			gotO++
			gotN++
		default:
			return h, 0, &FormatError{Line: ln + 1, Message: "hunk line must start with space, -, +, or \\"}
		}
		ln++
		// Optional no-newline marker attached to that body line.
		if ln < len(ls) && bytes.Equal(bytes.TrimRight(ls[ln], "\n"), []byte(noNL)) {
			ln++
		}
	}
	h.Ops = decodeOps(ls[start+1 : ln])
	consumed := ln - start
	return h, consumed, nil
}

func decodeOps(rows [][]byte) []edit.Op {
	var ops []edit.Op
	for _, row := range rows {
		if bytes.Equal(bytes.TrimRight(row, "\n"), []byte(noNL)) {
			continue
		}
		kind := edit.Equal
		switch row[0] {
		case '-':
			kind = edit.Delete
		case '+':
			kind = edit.Insert
		}
		inner := lines.Split(row[1:])
		var l lines.Line
		if len(inner) > 0 {
			l = inner[0]
		}
		ops = append(ops, edit.Op{Kind: kind, Line: l})
	}
	return ops
}

func parseHeader(row []byte) (os_, oc, ns, nc int, ok bool) {
	s := strings.TrimRight(string(row), "\n")
	if !strings.HasPrefix(s, "@@ ") || !strings.HasSuffix(s, " @@") {
		return 0, 0, 0, 0, false
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(s, "@@ "), " @@")
	parts := strings.Fields(mid)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "-") || !strings.HasPrefix(parts[1], "+") {
		return 0, 0, 0, 0, false
	}
	os_, oc, ok = parseRange(parts[0][1:])
	if !ok {
		return
	}
	ns, nc, ok = parseRange(parts[1][1:])
	return
}

func parseRange(s string) (start, count int, ok bool) {
	if i := strings.IndexByte(s, ','); i >= 0 {
		count, ok = atoi(s[i+1:])
		if !ok {
			return
		}
		s = s[:i]
	} else {
		count = 1
	}
	start, ok = atoi(s)
	return
}

func atoi(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func splitTextLines(data []byte) [][]byte {
	var out [][]byte
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			out = append(out, data)
			break
		}
		out = append(out, data[:i+1])
		data = data[i+1:]
	}
	return out
}
