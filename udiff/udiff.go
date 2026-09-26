// Package udiff 渲染与解析 unified diff 文本。
package udiff

import (
	"fmt"
	"strconv"
	"strings"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

// Line 是 hunk 内一行。Kind 为 ' ' '-' '+'；NL 标记表示 EOF 无换行。
// CRLF 行的 Text 末尾保留 '\r'，以保证行尾原样还原。
type Line struct {
	Kind  byte
	Text  string
	OldNL bool
	NewNL bool
}

// Hunk 对应一个 @@ 段。
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Patch 是一份完整补丁。
type Patch struct {
	OldName string
	NewName string
	Hunks   []Hunk
}

// FormatError 是补丁文本格式错误，Line 为补丁文本中的 1 基行号。
type FormatError struct {
	Line    int
	Message string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("udiff: format error at line %d: %s", e.Line, e.Message)
}

const noNL = `\ No newline at end of file`

// Make 由 a、b 文本生成补丁（C 为上下文行数）。
func Make(a, b, oldName, newName string, c, maxD int) (*Patch, error) {
	la, lb := lines.Split(a), lines.Split(b)
	script, err := edit.Diff(la, lb, maxD)
	if err != nil {
		return nil, err
	}
	hs, _, _ := hunk.Build(script, c)
	p := &Patch{OldName: oldName, NewName: newName}
	for _, h := range hs {
		nh := Hunk{OldStart: h.OldStart, OldCount: h.OldCount, NewStart: h.NewStart, NewCount: h.NewCount}
		for oi, op := range h.Ops {
			payload := op.Line.Text
			if op.Line.Term == "\r\n" {
				payload += "\r"
			}
			ln := Line{Kind: op.Kind, Text: payload}
			if oi == len(h.Ops)-1 && op.Line.NoNL() {
				ln.OldNL = op.Kind == ' ' || op.Kind == '-'
				ln.NewNL = op.Kind == ' ' || op.Kind == '+'
			}
			nh.Lines = append(nh.Lines, ln)
		}
		p.Hunks = append(p.Hunks, nh)
	}
	return p, nil
}

// Render 将补丁渲染为 unified diff 文本。
func Render(p *Patch) string {
	var b strings.Builder
	b.WriteString("--- " + p.OldName + "\n")
	b.WriteString("+++ " + p.NewName + "\n")
	for _, h := range p.Hunks {
		b.WriteString("@@ -" + coord(h.OldStart, h.OldCount) + " +" + coord(h.NewStart, h.NewCount) + " @@\n")
		for _, l := range h.Lines {
			b.WriteByte(l.Kind)
			b.WriteString(l.Text)
			b.WriteByte('\n')
			if l.OldNL || l.NewNL {
				b.WriteString(noNL + "\n")
			}
		}
	}
	return b.String()
}

func coord(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Parse 严格解析 unified diff。行数不符、非法行首、残缺头均为 FormatError。
func Parse(text string) (*Patch, error) {
	ls := strings.Split(text, "\n")
	if len(ls) > 0 && ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	if len(ls) < 2 || !strings.HasPrefix(ls[0], "--- ") || !strings.HasPrefix(ls[1], "+++ ") {
		return nil, &FormatError{Line: 1, Message: "missing file headers"}
	}
	p := &Patch{OldName: ls[0][4:], NewName: ls[1][4:]}
	for i := 2; i < len(ls); {
		ln := i + 1
		h, err := parseHeader(ls[i], ln)
		if err != nil {
			return nil, err
		}
		i++
		for h.OldCount > 0 || h.NewCount > 0 {
			if i >= len(ls) {
				return nil, &FormatError{Line: ln, Message: "hunk line count mismatch"}
			}
			row := ls[i]
			if len(row) == 0 || (row[0] != ' ' && row[0] != '-' && row[0] != '+') {
				return nil, &FormatError{Line: i + 1, Message: "invalid hunk line prefix"}
			}
			l := Line{Kind: row[0], Text: row[1:]}
			h.Lines = append(h.Lines, l)
			i++
			if l.Kind == '-' || l.Kind == ' ' {
				h.OldCount--
			}
			if l.Kind == '+' || l.Kind == ' ' {
				h.NewCount--
			}
			cur := &h.Lines[len(h.Lines)-1]
			if i < len(ls) && ls[i] == noNL {
				i++
				cur.OldNL = cur.Kind == ' ' || cur.Kind == '-'
				cur.NewNL = cur.Kind == ' ' || cur.Kind == '+'
			} else if i < len(ls) && strings.HasPrefix(ls[i], `\`) {
				return nil, &FormatError{Line: i + 1, Message: "bad no-newline marker"}
			}
		}
	p.Hunks = append(p.Hunks, *h)
	}
	return p, nil
}

func parseHeader(s string, lineNo int) (*Hunk, error) {
	if !strings.HasPrefix(s, "@@ ") {
		return nil, &FormatError{Line: lineNo, Message: "expected hunk header"}
	}
	rest := strings.TrimPrefix(s, "@@ ")
	mid := strings.Index(rest, " @@")
	if mid < 0 {
		return nil, &FormatError{Line: lineNo, Message: "malformed hunk header"}
	}
	fields := strings.Fields(rest[:mid])
	if len(fields) != 2 || fields[0][0] != '-' || fields[1][0] != '+' {
		return nil, &FormatError{Line: lineNo, Message: "malformed hunk ranges"}
	}
	var h Hunk
	var err error
	if h.OldStart, h.OldCount, err = parseRange(fields[0][1:]); err != nil {
		return nil, &FormatError{Line: lineNo, Message: "bad old range"}
	}
	if h.NewStart, h.NewCount, err = parseRange(fields[1][1:]); err != nil {
		return nil, &FormatError{Line: lineNo, Message: "bad new range"}
	}
	return &h, nil
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
