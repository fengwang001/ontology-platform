// Package udiff 渲染与解析统一格式（unified diff）文本：
// "--- "/"+++ " 文件头、"@@ -a,b +c,d @@" hunk 头、" "/"-"/"+" 行以及
// "\\ No newline at end of file" 标记。
package udiff

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

const noNL = "\\ No newline at end of file"

// Hunk 是解析或待渲染的一个 hunk。
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Lines              []Line
}

// Line 是 hunk 体中的一行。Raw 为去掉前缀后的原始内容（含原始行尾，
// NoNL 为真时不含行尾）。
type Line struct {
	Kind edit.Kind
	Raw  []byte
	NoNL bool
}

// Patch 是单文件统一格式补丁。
type Patch struct {
	OldHeader []byte
	NewHeader []byte
	Hunks     []*Hunk
}

// FormatError 表示补丁文本本身的格式错误，Line 为补丁文本中的 1 基行号。
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("udiff: malformed patch at line %d: %s", e.Line, e.Msg)
}

// Render 把 hunk 列表渲染成统一格式文本。oh/nh 为完整的旧/新文件头行
//（不含末尾换行），nil 时使用占位头。
func Render(hs []*hunk.Hunk, oh, nh []byte) []byte {
	if oh == nil {
		oh = []byte("--- a/file")
	}
	if nh == nil {
		nh = []byte("+++ b/file")
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\n%s\n", oh, nh)
	for _, h := range hs {
		oc, nc := h.Counts()
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", rangeStr(h.OldStart, oc), rangeStr(h.NewStart, nc))
		lastOld, lastNew := -1, -1
		for i := len(h.Ops) - 1; i >= 0; i-- {
			k := h.Ops[i].Kind
			if lastOld < 0 && k != edit.Insert {
				lastOld = i
			}
			if lastNew < 0 && k != edit.Delete {
				lastNew = i
			}
		}
		for i, op := range h.Ops {
			prefix := byte(' ')
			switch op.Kind {
			case edit.Delete:
				prefix = '-'
			case edit.Insert:
				prefix = '+'
			}
			b.WriteByte(prefix)
			b.Write(op.Line)
			if (i == lastOld || i == lastNew) && !lines.HasNL(op.Line) {
				fmt.Fprintf(&b, "\n%s\n", noNL)
			}
		}
	}
	return b.Bytes()
}

func rangeStr(start, count int) string {
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}
