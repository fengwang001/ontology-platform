// Package udiff 渲染与解析统一格式（unified diff）文本。
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

const noNL = `\ No newline at end of file`

// File 是一份单文件统一 diff。
type File struct {
	OldName string
	NewName string
	Hunks   []hunk.Hunk
}

// Limits 限制解析时的总字节数与 hunk 数，0 表示不限制。
type Limits struct {
	MaxBytes int
	MaxHunks int
}

type FormatError struct{ line int }

func (e *FormatError) Error() string {
	return fmt.Sprintf("udiff: malformed patch at line %d", e.line)
}

// Line 返回补丁文本中的行号（1 起）。
func (e *FormatError) Line() int { return e.line }

// ErrTooLarge 是总字节数或 hunk 数超限的可判定错误。
var ErrTooLarge = errors.New("udiff: patch exceeds limits")

// Render 用上下文 C 渲染 a 到 b 的统一 diff；无差异时返回 nil。
func Render(oldName, newName string, a, b []byte, C int) ([]byte, error) {
	al, bl := lines.Split(a), lines.Split(b)
	ops, err := edit.Diff(al, bl, len(al)+len(bl))
	if err != nil {
		return nil, err
	}
	hs := hunk.Build(ops, C)
	if len(hs) == 0 {
		return nil, nil
	}
	var sb strings.Builder
	sb.WriteString("--- " + oldName + "\n")
	sb.WriteString("+++ " + newName + "\n")
	for i := range hs {
		writeHunk(&sb, &hs[i], al, bl)
	}
	return []byte(sb.String()), nil
}

func writeHunk(sb *strings.Builder, h *hunk.Hunk, al, bl []lines.Line) {
	sb.WriteString("@@ -" + rangeText(h.OldStart, h.OldCount) +
		" +" + rangeText(h.NewStart, h.NewCount) + " @@\n")
	var oi, ni int
	for _, op := range h.Ops {
		switch op.Kind {
		case edit.Equal:
			writeLine(sb, ' ', op.L, oi == len(al)-1)
			oi++
			ni++
		case edit.Delete:
			writeLine(sb, '-', op.L, oi == len(al)-1)
			oi++
		case edit.Insert:
			writeLine(sb, '+', op.L, ni == len(bl)-1)
			ni++
		}
	}
}

func writeLine(sb *strings.Builder, p byte, l lines.Line, last bool) {
	sb.WriteByte(p)
	sb.WriteString(l.Text)
	switch {
	case l.NL != "":
		sb.WriteString(l.NL)
	case last:
		sb.WriteString("\n" + noNL + "\n")
	default:
		sb.WriteByte('\n')
	}
}

func rangeText(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Parse 严格解析单文件统一 diff，hunk 声明行数必须与实际严格一致。
func Parse(p []byte, lim Limits) (*File, error) {
	if lim.MaxBytes > 0 && len(p) > lim.MaxBytes {
		return nil, ErrTooLarge
	}
	ls := lines.Split(p)
	if len(ls) < 2 || !strings.HasPrefix(ls[0].Text, "--- ") ||
		!strings.HasPrefix(ls[1].Text, "+++ ") {
		return nil, &FormatError{line: 1}
	}
	f := &File{
		OldName: strings.TrimPrefix(ls[0].Text, "--- "),
		NewName: strings.TrimPrefix(ls[1].Text, "+++ "),
	}
	for i := 2; i < len(ls); {
		if !strings.HasPrefix(ls[i].Text, "@@ ") {
			return nil, &FormatError{line: i + 1}
		}
		if lim.MaxHunks > 0 && len(f.Hunks)+1 > lim.MaxHunks {
			return nil, ErrTooLarge
		}
		hh, n, err := parseHunk(ls, i)
		if err != nil {
			return nil, err
		}
		f.Hunks = append(f.Hunks, hh)
		i += n
	}
	return f, nil
}

func parseHunk(ls []lines.Line, i int) (hunk.Hunk, int, error) {
	h := hunk.Hunk{}
	rest := strings.TrimPrefix(ls[i].Text, "@@ ")
	fields := strings.SplitN(rest, " @@", 2)
	if len(fields) != 2 || !strings.HasPrefix(fields[0], "-") {
		return h, 0, &FormatError{line: i + 1}
	}
	parts := strings.SplitN(strings.TrimPrefix(fields[0], "-"), " +", 2)
	if len(parts) != 2 {
		return h, 0, &FormatError{line: i + 1}
	}
	var err error
	if h.OldStart, h.OldCount, err = parseRange(parts[0]); err != nil {
		return h, 0, &FormatError{line: i + 1}
	}
	if h.NewStart, h.NewCount, err = parseRange(parts[1]); err != nil {
		return h, 0, &FormatError{line: i + 1}
	}
	var oc, nc int
	type raw struct {
		kind edit.Kind
		l    lines.Line
	}
	var raws []raw
	j := i + 1
	for j < len(ls) && !strings.HasPrefix(ls[j].Text, "@@ ") {
		t := ls[j].Text
		switch {
		case t == noNL:
			if len(raws) == 0 {
				return h, 0, &FormatError{line: j + 1}
			}
			raws[len(raws)-1].l.NL = ""
		case strings.HasPrefix(t, " "), t == " ":
			raws = append(raws, raw{edit.Equal, lines.Line{Text: t[1:], NL: ls[j].NL}})
			oc++
			nc++
		case strings.HasPrefix(t, "-"):
			raws = append(raws, raw{edit.Delete, lines.Line{Text: t[1:], NL: ls[j].NL}})
			oc++
		case strings.HasPrefix(t, "+"):
			raws = append(raws, raw{edit.Insert, lines.Line{Text: t[1:], NL: ls[j].NL}})
			nc++
		default:
			return h, 0, &FormatError{line: j + 1}
		}
		j++
	}
	if oc != h.OldCount || nc != h.NewCount {
		return h, 0, &FormatError{line: i + 1}
	}
	for _, r := range raws {
		h.Ops = append(h.Ops, edit.Op{Kind: r.kind, L: r.l})
	}
	return h, j - i, nil
}

func parseRange(s string) (start, count int, err error) {
	if !strings.Contains(s, ",") {
		start, err = strconv.Atoi(s)
		return start, 1, err
	}
	p := strings.SplitN(s, ",", 2)
	if start, err = strconv.Atoi(p[0]); err != nil {
		return 0, 0, err
	}
	count, err = strconv.Atoi(p[1])
	return start, count, err
}
