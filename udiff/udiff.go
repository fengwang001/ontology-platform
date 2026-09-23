// Package udiff 渲染与解析 unified diff 文本。
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

// Patch 是一份单文件 unified diff。
type Patch struct {
	OldName string
	NewName string
	Hunks   []hunk.Hunk
}

// FormatError 携带补丁文本中的行号；ErrFormat 是其哨兵类别。
type FormatError struct {
	Line int
	Msg  string
}

var ErrFormat = errors.New("malformed patch")

func (e *FormatError) Error() string {
	return fmt.Sprintf("malformed patch at line %d: %s", e.Line, e.Msg)
}
func (e *FormatError) Unwrap() error { return ErrFormat }

// Make 对 a、b 生成补丁（上下文 C 行，编辑距离上限 maxD）。
func Make(a, b []byte, oldName, newName string, c, maxD int) (*Patch, error) {
	la, lb := lines.Split(a), lines.Split(b)
	ops, err := edit.Diff(la, lb, maxD)
	if err != nil {
		return nil, err
	}
	return &Patch{OldName: oldName, NewName: newName,
		Hunks: hunk.Build(la, lb, ops, c)}, nil
}

// Render 把补丁渲染为 unified diff 字节。
func Render(p *Patch) []byte {
	var b strings.Builder
	b.WriteString("--- " + p.OldName + "\n")
	b.WriteString("+++ " + p.NewName + "\n")
	for _, h := range p.Hunks {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", rng(h.OldStart, h.OldLen),
			rng(h.NewStart, h.NewLen))
		for _, r := range h.Rows {
			b.WriteByte(r.Kind)
			b.WriteString(r.Line.Content)
			b.WriteString(r.Line.Term)
			if r.Line.NoNL() {
				b.WriteString("\n" + marker + "\n")
			}
		}
	}
	return []byte(b.String())
}

func rng(start, length int) string {
	if length == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(length)
}

const marker = "\\ No newline at end of file"

// Parse 严格解析 unified diff；MaxBytes/MaxHunks 为 0 表示不限。
func Parse(data []byte, maxBytes, maxHunks int) (*Patch, error) {
	if maxBytes > 0 && len(data) > maxBytes {
		return nil, &FormatError{Line: 1, Msg: "patch exceeds byte limit"}
	}
	text := strings.Split(string(data), "\n")
	// data 以 "\n" 结尾时 split 末元素为空，去掉这个由终结换行产生的元素。
	if len(text) > 0 && text[len(text)-1] == "" {
		text = text[:len(text)-1]
	}
	lineno := func(i int) int { return i + 1 }
	if len(text) < 2 || !strings.HasPrefix(text[0], "--- ") ||
		!strings.HasPrefix(text[1], "+++ ") {
		return nil, &FormatError{Line: 1, Msg: "missing file headers"}
	}
	p := &Patch{OldName: text[0][4:], NewName: text[1][4:]}
	i := 2
	for i < len(text) {
		ln := text[i]
		if !strings.HasPrefix(ln, "@@ ") {
			return nil, &FormatError{Line: lineno(i), Msg: "expected hunk header"}
		}
		os, ol, ns, nl, err := parseHead(ln)
		if err != nil {
			return nil, &FormatError{Line: lineno(i), Msg: err.Error()}
		}
		h := hunk.Hunk{OldStart: os, OldLen: ol, NewStart: ns, NewLen: nl}
		i++
		oc, nc := 0, 0
		// lastNoNL 记录上一条正文行无行尾；它后面必须紧跟 marker（除非已是补丁末尾）。
		for oc < ol || nc < nl {
			if i >= len(text) {
				return nil, &FormatError{Line: lineno(i-1) + 1, Msg: "truncated hunk body"}
			}
			s := text[i]
			if s == marker {
				return nil, &FormatError{Line: lineno(i), Msg: "unexpected no-newline marker"}
			}
			if len(s) == 0 || (s[0] != ' ' && s[0] != '-' && s[0] != '+') {
				return nil, &FormatError{Line: lineno(i), Msg: "invalid body line prefix"}
			}
			content := s[1:]
			term := "\n"
			// 预读下一行：无行尾正文行后必须是 marker（或补丁恰好结束）。
			nextIsMarker := i+1 < len(text) && text[i+1] == marker
			atEOF := i+1 == len(text)
			if !nextIsMarker && !atEOF {
				// 保留行尾：下一行若以 \r 结尾说明本行是 CRLF。
			}
			if nextIsMarker || atEOF {
				term = ""
				if !nextIsMarker && !atEOF {
					term = "\n"
				}
			}
			if strings.HasSuffix(content, "\r") && term == "\n" {
				content = content[:len(content)-1]
				term = "\r\n"
			}
			h.Rows = append(h.Rows, hunk.Row{Kind: s[0],
				Line: lines.Line{Content: content, Term: term}})
			if s[0] != '+' {
				oc++
			}
			if s[0] != '-' {
				nc++
			}
			i++
			if term == "" {
				if i < len(text) {
					if text[i] != marker {
						return nil, &FormatError{Line: lineno(i), Msg: "missing no-newline marker"}
					}
					i++ // 消费 marker
				}
			}
		}
		p.Hunks = append(p.Hunks, h)
		if maxHunks > 0 && len(p.Hunks) > maxHunks {
			return nil, &FormatError{Line: lineno(i - 1), Msg: "too many hunks"}
		}
	}
	return p, nil
}

func parseHead(s string) (os, ol, ns, nl int, err error) {
	rest := strings.TrimPrefix(s, "@@ ")
	end := strings.Index(rest, " @@")
	if end < 0 {
		return 0, 0, 0, 0, errors.New("bad hunk header")
	}
	parts := strings.Fields(rest[:end])
	if len(parts) != 2 || parts[0][0] != '-' || parts[1][0] != '+' {
		return 0, 0, 0, 0, errors.New("bad hunk header")
	}
	parseOne := func(x string) (int, int, error) {
		x = x[1:]
		st, le := x, 1
		if c := strings.IndexByte(x, ','); c >= 0 {
			st, le = x[:c], x[c+1:]
		}
		a, e1 := strconv.Atoi(st)
		b, e2 := strconv.Atoi(le)
		if e1 != nil || e2 != nil || a < 0 || b < 0 {
			return 0, 0, errors.New("bad hunk header numbers")
		}
		return a, b, nil
	}
	if os, ol, err = parseOne(parts[0]); err != nil {
		return
	}
	ns, nl, err = parseOne(parts[1])
	return
}

// Lines 暴露行切分，供 patch 包复用。
func Lines(b []byte) []lines.Line { return lines.Split(b) }
