// Package udiff 渲染与解析统一格式（unified diff）文本。
package udiff

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"ontology/hunk"
	"ontology/lines"
)

// ErrFormat 表示补丁文本格式错误（错误信息含补丁内行号）。
var ErrFormat = errors.New("补丁格式错误")

// ErrLimit 表示补丁超过字节数或 hunk 数上限。
var ErrLimit = errors.New("补丁超过资源上限")

// Patch 是一个单文件补丁。
type Patch struct {
	Hunks []hunk.Hunk
}

// Limits 是解析时的资源上限，0 表示不限。
type Limits struct {
	MaxBytes, MaxHunks int
}

var marker = []byte("\\ No newline at end of file\n")

// Render 把 hunk 序列渲染成统一格式文本。无改动时返回 nil。
func Render(hs []hunk.Hunk) []byte {
	if len(hs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	buf.WriteString("--- a\n+++ b\n")
	for _, h := range hs {
		buf.WriteString("@@ -" + rng(h.AStart, h.ACount) + " +" + rng(h.BStart, h.BCount) + " @@\n")
		for _, l := range h.Lines {
			buf.WriteByte(l.Kind)
			buf.Write(l.Text)
			if !lines.Terminated(l.Text) {
				buf.WriteString("\n\\ No newline at end of file\n")
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

var headRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@\n$`)

// Parse 严格解析统一格式文本：头声明的行数必须与实际恰好一致，
// 每行必须以 \n 结尾，行首必须是 ' '、'-'、'+' 或无换行标记。
func Parse(data []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, ErrLimit
	}
	p := &Patch{}
	if len(data) == 0 {
		return p, nil
	}
	ls := lines.Split(data)
	bad := func(i int) error { return fmt.Errorf("udiff: 第 %d 行: %w", i+1, ErrFormat) }
	for i, l := range ls {
		if !lines.Terminated(l) {
			return nil, bad(i)
		}
	}
	if len(ls) < 2 || !bytes.HasPrefix(ls[0], []byte("--- ")) || !bytes.HasPrefix(ls[1], []byte("+++ ")) {
		return nil, bad(0)
	}
	i := 2
	for i < len(ls) {
		m := headRe.FindSubmatch(ls[i])
		if m == nil {
			return nil, bad(i)
		}
		h := hunk.Hunk{
			AStart: atoi(m[1]), ACount: cnt(m[2]),
			BStart: atoi(m[3]), BCount: cnt(m[4]),
		}
		if (h.ACount > 0 && h.AStart == 0) || (h.BCount > 0 && h.BStart == 0) {
			return nil, bad(i)
		}
		i++
		needA, needB := h.ACount, h.BCount
		for needA > 0 || needB > 0 {
			if i >= len(ls) {
				return nil, bad(i)
			}
			kind := ls[i][0]
			if kind != ' ' && kind != '-' && kind != '+' {
				return nil, bad(i)
			}
			if kind != '+' {
				needA--
			}
			if kind != '-' {
				needB--
			}
			if needA < 0 || needB < 0 {
				return nil, bad(i)
			}
			text := ls[i][1:]
			i++
			if i < len(ls) && bytes.Equal(ls[i], marker) {
				text = text[:len(text)-1]
				i++
			}
			h.Lines = append(h.Lines, hunk.Line{Kind: kind, Text: text})
		}
		p.Hunks = append(p.Hunks, h)
		if lim.MaxHunks > 0 && len(p.Hunks) > lim.MaxHunks {
			return nil, ErrLimit
		}
	}
	return p, nil
}

func atoi(b []byte) int {
	n, _ := strconv.Atoi(string(b))
	return n
}

func cnt(b []byte) int {
	if len(b) == 0 {
		return 1
	}
	return atoi(b)
}
