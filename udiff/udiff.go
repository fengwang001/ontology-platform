// Package udiff 渲染与解析统一格式（unified diff）文本。
package udiff

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ontology/hunk"
)

// ErrFormat 是补丁文本本身的格式错误；*FormatError 实现 Unwrap 指向它。
var ErrFormat = errors.New("udiff: malformed patch")

// FormatError 带补丁文本中的 1 基行号与原因。
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string { return fmt.Sprintf("udiff: line %d: %s", e.Line, e.Msg) }
func (e *FormatError) Unwrap() error { return ErrFormat }

func ferr(n int, msg string) *FormatError { return &FormatError{Line: n, Msg: msg} }

// Patch 是一份单文档统一格式补丁。
type Patch struct {
	OldName string
	NewName string
	Hunks   []*hunk.Hunk
}

// Options 控制渲染上下文与解析资源上限。MaxBytes/MaxHunks<=0 表示不限。
type Options struct {
	Context  int
	MaxBytes int
	MaxHunks int
}

// Render 把 hunk 列表渲染为统一格式文本；name 为两侧文件名。
func Render(p *Patch, c int) string {
	var b strings.Builder
	b.WriteString("--- " + p.OldName + "\n")
	b.WriteString("+++ " + p.NewName + "\n")
	for _, h := range p.Hunks {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", rng(h.OStart, h.OCount), rng(h.NStart, h.NCount))
		lastO, lastN := -1, -1
		for i, it := range h.Items {
			if it.Kind != hunk.Add {
				lastO = i
			}
			if it.Kind != hunk.Remove {
				lastN = i
			}
		}
		for i, it := range h.Items {
			switch it.Kind {
			case hunk.Context:
				b.WriteByte(' ')
				b.WriteString(it.OL)
			case hunk.Remove:
				b.WriteByte('-')
				b.WriteString(it.OL)
			case hunk.Add:
				b.WriteByte('+')
				b.WriteString(it.NL)
			}
			if h.OldNoNL && i == lastO {
				b.WriteString("\\ No newline at end of file\n")
			}
			if h.NewNoNL && i == lastN {
				b.WriteString("\\ No newline at end of file\n")
			}
		}
	}
	return b.String()
}

func rng(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Parse 严格解析统一格式文本。头声明行数必须与实际一致；Options 的上限超限
// 返回 FormatError。截断文本同样以 FormatError 拒绝，绝不 panic。
func Parse(text string, o Options) (*Patch, error) {
	if o.MaxBytes > 0 && len(text) > o.MaxBytes {
		return nil, ferr(0, "patch exceeds byte limit")
	}
	ls := strings.Split(text, "\n")
	if n := len(ls); n > 0 && ls[n-1] == "" {
		ls = ls[:n-1]
	}
	if len(ls) < 2 || !strings.HasPrefix(ls[0], "--- ") || !strings.HasPrefix(ls[1], "+++ ") {
		return nil, ferr(1, "missing file headers")
	}
	p := &Patch{OldName: ls[0][4:], NewName: ls[1][4:]}
	for i := 2; i < len(ls); {
		if o.MaxHunks > 0 && len(p.Hunks) >= o.MaxHunks {
			return nil, ferr(i+1, "too many hunks")
		}
		if !strings.HasPrefix(ls[i], "@@ ") {
			return nil, ferr(i+1, "expected hunk header")
		}
		h, nxt, err := parseHunk(ls, i)
		if err != nil {
			return nil, err
		}
		p.Hunks = append(p.Hunks, h)
		i = nxt
	}
	return p, nil
}

func parseHunk(ls []string, i int) (*hunk.Hunk, int, error) {
	h, err := parseHeader(ls[i], i+1)
	if err != nil {
		return nil, 0, err
	}
	j := i + 1
	oc, nc := 0, 0
	for (oc < h.OCount || nc < h.NCount) && j < len(ls) {
		line := ls[j]
		raw := line[1:]
		if j+1 >= len(ls) || !strings.HasPrefix(ls[j+1], "\\ ") {
			raw += "\n" // 内容行在补丁文本中自带换行；下一标记行表示无换行
		}
		if line == "" {
			return nil, 0, ferr(j+1, "unexpected empty line in hunk")
		}
		switch line[0] {
		case ' ':
			h.Items = append(h.Items, hunk.Item{Kind: hunk.Context, OL: raw, NL: raw})
			oc++
			nc++
		case '-':
			h.Items = append(h.Items, hunk.Item{Kind: hunk.Remove, OL: raw})
			oc++
		case '+':
			h.Items = append(h.Items, hunk.Item{Kind: hunk.Add, NL: raw})
			nc++
		default:
			return nil, 0, ferr(j+1, "bad hunk line prefix")
		}
		j++
	}
	if oc != h.OCount || nc != h.NCount {
		return nil, 0, ferr(j+1, "hunk truncated: declared line count mismatch")
	}
	// 紧随内容的无换行标记：每个 hunk 每侧至多一个，且只能跟在末条内容行后。
	lastO, lastN := -1, -1
	for k, it := range h.Items {
		if it.Kind != hunk.Add {
			lastO = k
		}
		if it.Kind != hunk.Remove {
			lastN = k
		}
	}
	end := len(h.Items) - 1
	marker := func(side bool) error {
		if j >= len(ls) || ls[j] != "\\ No newline at end of file" {
			return ferr(j+1, "missing no-newline marker")
		}
		j++
		if side {
			h.OldNoNL = true
		} else {
			h.NewNoNL = true
		}
		return nil
	}
	if len(h.Items) > 0 && lastO == end {
		if err := marker(true); err != nil {
			return nil, 0, err
		}
	}
	if len(h.Items) > 0 && lastN == end {
		if err := marker(false); err != nil {
			return nil, 0, err
		}
	}
	return h, j, nil
}

func parseHeader(s string, ln int) (*hunk.Hunk, error) {
	rest, ok := strings.CutPrefix(s, "@@ ")
	if !ok || !strings.HasSuffix(rest, " @@") {
		return nil, ferr(ln, "bad hunk header")
	}
	mid := rest[:len(rest)-3]
	oldPart, newPart, ok := strings.Cut(mid, " ")
	if !ok {
		return nil, ferr(ln, "bad hunk header")
	}
	os, oc, err := parseRange(oldPart)
	if err != nil {
		return nil, ferr(ln, err.Error())
	}
	ns, nc, err2 := parseRange(newPart)
	if err2 != nil {
		return nil, ferr(ln, err2.Error())
	}
	return &hunk.Hunk{OStart: os, OCount: oc, NStart: ns, NCount: nc}, nil
}

func parseRange(s string) (start, count int, err error) {
	sign := s[0]
	if sign != '-' && sign != '+' {
		return 0, 0, errors.New("bad range")
	}
	num := s[1:]
	a, b, hasComma := strings.Cut(num, ",")
	start, err = strconv.Atoi(a)
	if err != nil {
		return 0, 0, errors.New("bad range number")
	}
	if !hasComma {
		return start, 1, nil
	}
	count, err = strconv.Atoi(b)
	if err != nil {
		return 0, 0, errors.New("bad range count")
	}
	return start, count, nil
}
