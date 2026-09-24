// Package udiff 渲染并严格解析 unified diff 文本。
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

// NoNLMarker 是“无行尾”标记行的完整文本。
const NoNLMarker = `\ No newline at end of file`

const (
	OldHeader = "--- old\n"
	NewHeader = "+++ new\n"
)

// Row 是解析/渲染的一行：Kind 为 ' '/'-'/'+'; Line 保留原始内容与行尾。
// NoNL 表示这一行对应侧没有行尾（其后必须出现标记行）。
type Row struct {
	Kind byte
	Line lines.Line
	NoNL bool
}

// Hunk 是解析后的 hunk，记录声明的 1 基行号与计数。
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Rows               []Row
}

// Patch 是一份单文件补丁。
type Patch struct {
	OldName, NewName string
	Hunks            []*Hunk
}

// FormatError 是补丁文本格式错误；Line 为补丁中的 1 基行号。
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("udiff: format error at line %d: %s", e.Line, e.Msg)
}

// Limits 限制解析时的字节数与 hunk 数；0 表示不限制。
type Limits struct {
	MaxBytes int
	MaxHunks int
}

// ErrTooLarge 表示补丁超过资源上限。
var ErrTooLarge = errors.New("udiff: patch exceeds size or hunk limit")

// Render 用上下文 c 生成 a→b 的统一格式补丁。
func Render(a, b []byte, c int) ([]byte, error) {
	al, bl := lines.Split(a), lines.Split(b)
	script, err := edit.Diff(al, bl)
	if err != nil {
		return nil, err
	}
	hs := hunk.Build(script, al, bl, c)
	var sb strings.Builder
	sb.WriteString(OldHeader)
	sb.WriteString(NewHeader)
	for _, hh := range hs {
		sb.WriteString(renderHeader(hh.OldStart, hh.OldCount, hh.NewStart, hh.NewCount))
		for _, r := range hh.Rows {
			k := r.Kind
			if k == '=' {
				k = ' '
			}
			writeRow(&sb, k, pick(al, bl, r))
			if r.OldNoNL && r.Kind != '+' {
				sb.WriteString(NoNLMarker + "\n")
			}
			if r.NewNoNL && r.Kind != '-' {
				sb.WriteString(NoNLMarker + "\n")
			}
		}
	}
	return []byte(sb.String()), nil
}

func pick(a, b []lines.Line, r hunk.Row) lines.Line {
	if r.Kind == '+' {
		return b[r.Idx]
	}
	return a[r.Idx]
}

func writeRow(sb *strings.Builder, kind byte, l lines.Line) {
	sb.WriteByte(kind)
	sb.Write(l.Data)
	sb.Write(l.End)
}

func renderHeader(os, oc, ns, nc int) string {
	return "@@ -" + part(os, oc) + " +" + part(ns, nc) + " @@\n"
}

func part(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Parse 严格解析补丁文本；任何不一致都返回 *FormatError。
func Parse(data []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, ErrTooLarge
	}
	text := string(data)
	rawLines := splitPatch(text)
	if len(rawLines) < 2 || rawLines[0].text != strings.TrimSuffix(OldHeader, "\n") ||
		rawLines[1].text != strings.TrimSuffix(NewHeader, "\n") {
		return nil, &FormatError{Line: minLine(len(rawLines)), Msg: "missing or bad ---/+++ headers"}
	}
	p := &Patch{OldName: "old", NewName: "new"}
	i := 2
	for i < len(rawLines) {
		if lim.MaxHunks > 0 && len(p.Hunks) >= lim.MaxHunks {
			return nil, ErrTooLarge
		}
		h, next, err := parseHunk(rawLines, i)
		if err != nil {
			return nil, err
		}
		p.Hunks = append(p.Hunks, h)
		i = next
	}
	return p, nil
}

type pline struct {
	text string // 不含最终 \n
	cr   bool   // 该行以 \r\n 结束
	last bool   // 最后一行且无 \n
	no   int    // 1 基补丁行号
}

func splitPatch(s string) []pline {
	var out []pline
	no := 1
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, pline{text: s, last: true, no: no})
			break
		}
		line := s[:i]
		cr := strings.HasSuffix(line, "\r")
		if cr {
			line = strings.TrimSuffix(line, "\r")
		}
		out = append(out, pline{text: line, cr: cr, no: no})
		s = s[i+1:]
		no++
	}
	return out
}

func parseHunk(ls []pline, i int) (*Hunk, int, error) {
	os, oc, ns, nc, err := parseHeader(ls[i].text)
	if err != nil {
		return nil, 0, &FormatError{Line: ls[i].no, Msg: err.Error()}
	}
	h := &Hunk{OldStart: os, OldCount: oc, NewStart: ns, NewCount: nc}
	i++
	ocSeen, ncSeen := 0, 0
	for ocSeen < oc || ncSeen < nc {
		if i >= len(ls) {
			return nil, 0, &FormatError{Line: lastNo(ls), Msg: "truncated hunk body"}
		}
		cur := ls[i]
		if cur.text == NoNLMarker {
			if len(h.Rows) == 0 {
				return nil, 0, &FormatError{Line: cur.no, Msg: "marker without preceding row"}
			}
			mark(&h.Rows[len(h.Rows)-1])
			i++
			continue
		}
		if len(cur.text) == 0 {
			return nil, 0, &FormatError{Line: cur.no, Msg: "row must start with space, -, or +"}
		}
		k, body := cur.text[0], cur.text[1:]
		if k != ' ' && k != '-' && k != '+' {
			return nil, 0, &FormatError{Line: cur.no, Msg: "bad row prefix"}
		}
		end := []byte{'\n'}
		if cur.cr {
			end = []byte{'\r', '\n'}
		}
		if cur.last {
			end = nil
		}
		h.Rows = append(h.Rows, Row{Kind: k, Line: lines.Line{Data: []byte(body), End: end}})
		if k != '+' {
			ocSeen++
		}
		if k != '-' {
			ncSeen++
		}
		i++
	}
	for i < len(ls) && ls[i].text == NoNLMarker {
		if len(h.Rows) == 0 {
			return nil, 0, &FormatError{Line: ls[i].no, Msg: "marker without preceding row"}
		}
		mark(&h.Rows[len(h.Rows)-1])
		i++
	}
	return h, i, nil
}

func mark(r *Row) {
	if r.Kind != '+' {
		r.NoNL = true
		r.Line.End = nil
	}
	if r.Kind != '-' {
		r.NoNL = true
		r.Line.End = nil
	}
}

func parseHeader(s string) (os, oc, ns, nc int, err error) {
	if !strings.HasPrefix(s, "@@ -") || !strings.HasSuffix(s, " @@") {
		return 0, 0, 0, 0, errors.New("bad hunk header")
	}
	mid := s[4 : len(s)-3+0]
	plus := strings.Index(mid, " +")
	if plus < 0 {
		return 0, 0, 0, 0, errors.New("bad hunk header")
	}
	oldp, newp := mid[:plus], mid[plus+1:]
	if os, oc, err = parsePart(oldp); err != nil {
		return
	}
	ns, nc, err = parsePart("+" + newp)
	return
}

func parsePart(p string) (start, count int, err error) {
	if !strings.HasPrefix(p, "-") && !strings.HasPrefix(p, "+") {
		return 0, 0, errors.New("bad range")
	}
	p = p[1:]
	if i := strings.IndexByte(p, ','); i >= 0 {
		if start, err = strconv.Atoi(p[:i]); err != nil {
			return 0, 0, errors.New("bad number")
		}
		if count, err = strconv.Atoi(p[i+1:]); err != nil {
			return 0, 0, errors.New("bad number")
		}
		return start, count, nil
	}
	if start, err = strconv.Atoi(p); err != nil {
		return 0, 0, errors.New("bad number")
	}
	return start, 1, nil
}

func minLine(n int) int {
	if n == 0 {
		return 1
	}
	return n
}

func lastNo(ls []pline) int {
	if len(ls) == 0 {
		return 1
	}
	return ls[len(ls)-1].no
}
