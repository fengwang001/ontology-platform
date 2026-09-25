// Package udiff 渲染与解析统一格式（unified diff）文本。
package udiff

import (
	"bytes"
	"errors"
	"fmt"
	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"regexp"
	"strconv"
	"strings"
)

var ErrFormat = errors.New("udiff: malformed patch")        // 补丁文本格式错误
var ErrTooLarge = errors.New("udiff: patch exceeds limits") // 超过字节数或 hunk 数上限

// LineError 定位补丁文本中出错的行（1-based）。
type LineError struct {
	Line int
	Err  error
}

func (e *LineError) Error() string { return fmt.Sprintf("line %d: %v", e.Line, e.Err) }
func (e *LineError) Unwrap() error { return e.Err }

// Limits 为解析上限，0 表示该项不限。
type Limits struct{ MaxBytes, MaxHunks int }

// Patch 是一个文件的补丁：文件头加若干 hunk。
type Patch struct {
	OldName, NewName string
	Hunks            []hunk.Hunk
}

// Diff 计算 a→b 的 hunk 序列（上下文 ctx 行，距离上限 maxDist，负值不限）。
func Diff(a, b []byte, ctx, maxDist int) ([]hunk.Hunk, error) {
	s, err := edit.Diff(lines.Split(a), lines.Split(b), maxDist)
	if err != nil {
		return nil, err
	}
	return hunk.Group(s, ctx), nil
}

// Render 把 hunk 序列渲染为统一格式文本。
func Render(oldName, newName string, hs []hunk.Hunk) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--- %s\n+++ %s\n", oldName, newName)
	for _, h := range hs {
		fmt.Fprintf(&buf, "@@ -%s +%s @@\n", rng(h.OldStart, h.OldCount), rng(h.NewStart, h.NewCount))
		for _, l := range h.Lines {
			buf.WriteByte(l.Kind)
			buf.Write(l.Text)
			if !bytes.HasSuffix(l.Text, []byte("\n")) {
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
	return fmt.Sprintf("%d,%d", start, count)
}

var headerRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// Parse 严格解析统一格式文本：hunk 头声明的行数必须与实际恰好一致。
func Parse(data []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, ErrTooLarge
	}
	rows := lines.Split(data)
	bad := func(i int) error { return &LineError{Line: i + 1, Err: ErrFormat} }
	for i, pre := range []string{"--- ", "+++ "} {
		if i >= len(rows) || !strings.HasPrefix(string(rows[i]), pre) {
			return nil, bad(i)
		}
	}
	name := func(i int) string { return strings.TrimSuffix(string(rows[i][4:]), "\n") }
	p := &Patch{OldName: name(0), NewName: name(1)}
	i := 2
	for i < len(rows) {
		m := headerRe.FindStringSubmatch(string(rows[i]))
		if m == nil {
			return nil, bad(i)
		}
		h := hunk.Hunk{OldStart: num(m[1], 0), OldCount: num(m[2], 1), NewStart: num(m[3], 0), NewCount: num(m[4], 1)}
		i++
		oldSeen, newSeen := 0, 0
		for oldSeen < h.OldCount || newSeen < h.NewCount {
			if i >= len(rows) || len(rows[i]) == 0 {
				return nil, bad(i)
			}
			kind := rows[i][0]
			if kind != '+' {
				oldSeen++
			}
			if kind != '-' {
				newSeen++
			}
			if (kind != ' ' && kind != '-' && kind != '+') || oldSeen > h.OldCount || newSeen > h.NewCount {
				return nil, bad(i)
			}
			text := rows[i][1:]
			if i+1 < len(rows) && rows[i+1][0] == '\\' {
				text = bytes.TrimSuffix(text, []byte("\n"))
				i++
			} else if !bytes.HasSuffix(text, []byte("\n")) {
				return nil, bad(i) // 内容行无行尾且无标记：截断
			}
			h.Lines = append(h.Lines, hunk.Line{Kind: kind, Text: text})
			i++
		}
		p.Hunks = append(p.Hunks, h)
		if lim.MaxHunks > 0 && len(p.Hunks) > lim.MaxHunks {
			return nil, ErrTooLarge
		}
	}
	return p, nil
}

func num(s string, def int) int {
	if s == "" {
		return def
	}
	n, _ := strconv.Atoi(s)
	return n
}

// Invert 返回交换旧/新两侧后的新补丁（用于反向应用）。
func (p *Patch) Invert() *Patch {
	q := &Patch{OldName: p.NewName, NewName: p.OldName, Hunks: make([]hunk.Hunk, len(p.Hunks))}
	for i, h := range p.Hunks {
		h.OldStart, h.NewStart, h.OldCount, h.NewCount = h.NewStart, h.OldStart, h.NewCount, h.OldCount
		h.Lines = append([]hunk.Line(nil), h.Lines...)
		for j, l := range h.Lines {
			if l.Kind == '-' || l.Kind == '+' {
				h.Lines[j].Kind = '-' + '+' - l.Kind
			}
		}
		q.Hunks[i] = h
	}
	return q
}
