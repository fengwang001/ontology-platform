// Package udiff 渲染与解析 unified diff 文本。
package udiff

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

const noNL = `\ No newline at end of file`

// Hunk 是解析/渲染后的一个 hunk。
type Hunk struct {
	OldStart, NewStart int
	OldCount, NewCount int
	Body               []Line
}

// Line 是 hunk 体里的一行。Kind 为 ' '、'-'、'+'；NoNL 表示该行后紧跟无换行标记。
type Line struct {
	Kind byte
	Text lines.Line
	NoNL bool
}

// Patch 是一份 unified diff。
type Patch struct {
	OldName, NewName string
	Hunks            []*Hunk
}

// FormatError 是补丁文本本身的格式错误，LineNo 为补丁文本中的行号（1 基）。
type FormatError struct {
	LineNo int
	Msg    string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("udiff: malformed patch at line %d: %s", e.LineNo, e.Msg)
}

var hunkHead = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@$`)

// Render 把脚本渲染为 unified diff 文本。oldName/newName 为文件头名字。
func Render(items []edit.Item, oldName, newName string, context int) []byte {
	return RenderHunks(hunk.Build(items, context), oldName, newName)
}

// RenderHunks 渲染已分组的 hunk。
func RenderHunks(hs []hunk.Hunk, oldName, newName string) []byte {
	var b strings.Builder
	b.WriteString("--- " + oldName + "\n")
	b.WriteString("+++ " + newName + "\n")
	for _, h := range hs {
		b.WriteString(fmt.Sprintf("@@ -%s +%s @@\n", rangeSpec(h.OldStart, h.OldCount), rangeSpec(h.NewStart, h.NewCount)))
		writeBody(&b, h.Items)
	}
	return []byte(b.String())
}

func writeBody(b *strings.Builder, items []edit.Item) {
	for i, it := range items {
		var kind byte
		var l lines.Line
		switch it.Op {
		case edit.Equal:
			kind, l = ' ', it.Old
		case edit.Delete:
			kind, l = '-', it.Old
		case edit.Insert:
			kind, l = '+', it.New
		}
		b.WriteByte(kind)
		b.Write(l.Content)
		if len(l.NL) > 0 {
			b.Write(l.NL)
		} else {
			b.WriteByte('\n')
			b.WriteString(noNL)
			if i != len(items)-1 {
				b.WriteByte('\n')
			}
		}
	}
}

func rangeSpec(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// Parse 严格解析 unified diff 文本。声明行数与实际行数不一致、行首非法等均返回 *FormatError。
func Parse(data []byte) (*Patch, error) {
	ls := lines.Split(data)
	if len(ls) < 2 {
		return nil, &FormatError{LineNo: len(ls) + 1, Msg: "missing file headers"}
	}
	p := &Patch{}
	for i := 0; i < 2; i++ {
		c := string(ls[i].Content)
		if len(c) < 4 {
			return nil, &FormatError{LineNo: i + 1, Msg: "bad file header"}
		}
		name := c[4:]
		if i == 0 {
			if c[:4] != "--- " {
				return nil, &FormatError{LineNo: i + 1, Msg: "missing --- header"}
			}
			p.OldName = name
		} else {
			if c[:4] != "+++ " {
				return nil, &FormatError{LineNo: i + 1, Msg: "missing +++ header"}
			}
			p.NewName = name
		}
	}
	idx := 2
	for idx < len(ls) {
		h, consumed, err := parseHunk(ls, idx)
		if err != nil {
			return nil, err
		}
		p.Hunks = append(p.Hunks, h)
		idx += consumed
	}
	return p, nil
}

func parseHunk(ls []lines.Line, idx int) (*Hunk, int, error) {
	m := hunkHead.FindStringSubmatch(string(ls[idx].Content))
	if m == nil {
		return nil, 0, &FormatError{LineNo: idx + 1, Msg: "bad hunk header"}
	}
	h := &Hunk{}
	h.OldStart, _ = strconv.Atoi(m[1])
	h.NewStart, _ = strconv.Atoi(m[3])
	h.OldCount = 1
	if m[2] != "" {
		h.OldCount, _ = strconv.Atoi(m[2])
	}
	h.NewCount = 1
	if m[4] != "" {
		h.NewCount, _ = strconv.Atoi(m[4])
	}
	oc, nc, i := 0, 0, idx+1
	for i < len(ls) && oc < h.OldCount || i < len(ls) && nc < h.NewCount {
		c := ls[i].Content
		if len(c) == 0 || (c[0] != ' ' && c[0] != '-' && c[0] != '+') {
			return nil, 0, &FormatError{LineNo: i + 1, Msg: "bad hunk body prefix"}
		}
		nl := append([]byte(nil), ls[i].NL...)
		line := Line{Kind: c[0], Text: lines.Line{Content: append([]byte(nil), c[1:]...), NL: nl}}
		if len(nl) == 0 {
			if i+1 >= len(ls) || string(ls[i+1].Content) != noNL {
				return nil, 0, &FormatError{LineNo: i + 1, Msg: "missing newline marker"}
			}
			line.NoNL = true
			i++
		}
		if line.Kind != '+' {
			oc++
		}
		if line.Kind != '-' {
			nc++
		}
		h.Body = append(h.Body, line)
		i++
	}
	if oc != h.OldCount || nc != h.NewCount {
		return nil, 0, &FormatError{LineNo: i + 1, Msg: "hunk line count mismatch"}
	}
	if i < len(ls) && strings.HasPrefix(string(ls[i].Content), `\`) {
		return nil, 0, &FormatError{LineNo: i + 1, Msg: "stray no-newline marker"}
	}
	return h, i - idx, nil
}

// IsFormatError 报告 err 是否为补丁格式错误。
func IsFormatError(err error) bool {
	var fe *FormatError
	return errors.As(err, &fe)
}
