// Package udiff 渲染与解析统一格式（unified diff）文本。
package udiff

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

// ErrFormat 表示补丁文本格式错误（含超上限），可用 errors.Is 判定。
var ErrFormat = errors.New("udiff: malformed patch")

// ParseError 带补丁文本中的 1 基行号。
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return fmt.Sprintf("udiff: line %d: %s", e.Line, e.Msg) }
func (e *ParseError) Unwrap() error { return ErrFormat }

// Limits 限制补丁规模；0 表示不限制。
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// Patch 是解析后的单文件补丁。
type Patch struct {
	Hunks []hunk.Hunk
}

// Diff 计算 a、b 的最短编辑脚本并按上下文行数 ctx 分组为 hunk。
// maxDist >= 0 时为编辑距离上限，超过返回 edit.ErrTooLarge。
func Diff(a, b []byte, ctx, maxDist int) ([]hunk.Hunk, error) {
	la, lb := lines.Split(a), lines.Split(b)
	ops, err := edit.Diff(la, lb, maxDist)
	if err != nil {
		return nil, err
	}
	return hunk.Group(ops, la, lb, ctx), nil
}

// Render 把 hunk 序列渲染为统一格式文本。
func Render(hs []hunk.Hunk) []byte {
	var buf bytes.Buffer
	buf.WriteString("--- a\n+++ b\n")
	for _, h := range hs {
		fmt.Fprintf(&buf, "@@ -%s +%s @@\n", rng(h.OldStart, h.OldCount), rng(h.NewStart, h.NewCount))
		for _, l := range h.Lines {
			buf.WriteByte(l.Kind)
			buf.WriteString(l.Text)
			if !strings.HasSuffix(l.Text, "\n") {
				buf.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return buf.Bytes()
}

// rng 把 0 基起始下标与计数格式化为 GNU 风格区间：计数 0 写前一行行号，计数 1 省略 ,1。
func rng(start, count int) string {
	n := start + 1
	if count == 0 {
		n = start
	}
	if count == 1 {
		return strconv.Itoa(n)
	}
	return strconv.Itoa(n) + "," + strconv.Itoa(count)
}

var hdrRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@$`)

// Parse 严格解析补丁文本：头部声明行数必须与实际行数恰好一致。
func Parse(data []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, &ParseError{0, "patch exceeds byte limit"}
	}
	ls := strings.Split(string(data), "\n")
	if len(ls) > 0 && ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	if len(ls) < 2 || !strings.HasPrefix(ls[0], "--- ") || !strings.HasPrefix(ls[1], "+++ ") {
		return nil, &ParseError{1, "missing ---/+++ header"}
	}
	p := &Patch{}
	i := 2
	for i < len(ls) {
		m := hdrRe.FindStringSubmatch(ls[i])
		if m == nil {
			return nil, &ParseError{i + 1, "expected @@ hunk header"}
		}
		if lim.MaxHunks > 0 && len(p.Hunks) >= lim.MaxHunks {
			return nil, &ParseError{i + 1, "too many hunks"}
		}
		h := hunk.Hunk{OldStart: atoi(m[1]), OldCount: cnt(m[2]), NewStart: atoi(m[3]), NewCount: cnt(m[4])}
		if h.OldCount > 0 {
			h.OldStart--
		}
		if h.NewCount > 0 {
			h.NewStart--
		}
		i++
		needOld, needNew := h.OldCount, h.NewCount
		for needOld > 0 || needNew > 0 {
			if i >= len(ls) {
				return nil, &ParseError{i + 1, "hunk body shorter than header declares"}
			}
			l := ls[i]
			i++
			if l == "" {
				return nil, &ParseError{i, "empty line in hunk body"}
			}
			switch l[0] {
			case '\\':
				return nil, &ParseError{i, `stray \ marker`}
			case ' ', '-', '+':
				k := l[0]
				if k != '+' {
					needOld--
				}
				if k != '-' {
					needNew--
				}
				if needOld < 0 || needNew < 0 {
					return nil, &ParseError{i, "hunk body longer than header declares"}
				}
				h.Lines = append(h.Lines, hunk.Line{Kind: k, Text: l[1:] + "\n"})
			default:
				return nil, &ParseError{i, "line must start with space, -, + or \\"}
			}
			for i < len(ls) && strings.HasPrefix(ls[i], "\\") {
				h.Lines[len(h.Lines)-1].Text = strings.TrimSuffix(h.Lines[len(h.Lines)-1].Text, "\n")
				i++
			}
		}
		p.Hunks = append(p.Hunks, h)
	}
	return p, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func cnt(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}
