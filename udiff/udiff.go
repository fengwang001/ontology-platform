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

// ErrFormat 是补丁文本格式错误；Line 为补丁文本中的行号（1 起）。
type ErrFormat struct{ Line int; Msg string }

func (e *ErrFormat) Error() string { return fmt.Sprintf("malformed patch at line %d: %s", e.Line, e.Msg) }

// Limits 限制补丁总字节数与 hunk 数，<=0 表示不限制。
type Limits struct{ MaxBytes, MaxHunks int }

// Patch 是解析后的补丁。
type Patch struct {
	OldPath, NewPath string
	Hunks            []hunk.Hunk
}

const noNL = `\ No newline at end of file`

// Render 渲染 hunk 列表为 unified diff 文本。
func Render(oldPath, newPath string, hs []hunk.Hunk) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", oldPath, newPath)
	for _, h := range hs {
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", loc(h.OldStart, h.OldCount), loc(h.NewStart, h.NewCount))
		for _, r := range h.Rows {
			b.WriteByte(byte(r.Kind))
			b.WriteString(r.Text)
			if r.Kind == '+' {
				b.WriteString(r.NewEnd)
			} else if r.Kind == '-' {
				b.WriteString(r.OldEnd)
			} else {
				b.WriteString(r.OldEnd)
			}
			if r.Kind != '+' && r.OldEnd == "" {
				b.WriteString(noNL + "\n")
			}
			if r.Kind != '-' && r.NewEnd == "" && r.OldEnd != "" {
				b.WriteString(noNL + "\n")
			}
		}
	}
	return []byte(b.String())
}

func loc(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// Make 从两段字节串生成完整补丁文本；maxD<0 不设编辑距离上限。
func Make(a, b []byte, context, maxD int) ([]byte, error) {
	ops, err := edit.Script(lines.Split(a), lines.Split(b), maxD)
	if err != nil {
		return nil, err
	}
	return Render("file", "file", hunk.Group(ops, context)), nil
}

// Parse 严格解析补丁文本。
func Parse(data []byte, lim Limits) (*Patch, error) {
	if lim.MaxBytes > 0 && len(data) > lim.MaxBytes {
		return nil, &ErrFormat{Line: 1, Msg: "patch exceeds byte limit"}
	}
	ls := splitLF(data)
	if len(ls) < 2 || !strings.HasPrefix(ls[0].text, "--- ") || !strings.HasPrefix(ls[1].text, "+++ ") {
		return nil, &ErrFormat{Line: len(ls), Msg: "missing ---/+++ headers"}
	}
	p := &Patch{OldPath: strings.TrimPrefix(ls[0].text, "--- "), NewPath: strings.TrimPrefix(ls[1].text, "+++ ")}
	i := 2
	for i < len(ls) {
		ln := i + 1
		hdr := ls[i].text
		if !strings.HasPrefix(hdr, "@@ ") {
			return nil, &ErrFormat{Line: ln, Msg: "expected hunk header"}
		}
		os, oc, ns, nc, perr := parseHdr(hdr, ln)
		if perr != nil {
			return nil, perr
		}
		h := hunk.Hunk{OldStart: os, OldCount: oc, NewStart: ns, NewCount: nc}
		i++
		goc, gnc := 0, 0
		for goc < oc || gnc < nc {
			if i >= len(ls) {
				return nil, &ErrFormat{Line: ln, Msg: "hunk truncated"}
			}
			t := ls[i].text
			if t == noNL {
				return nil, &ErrFormat{Line: i + 1, Msg: "stray no-newline marker"}
			}
			if len(t) == 0 || (len(t) == 1 && t[0] == '\\') {
				return nil, &ErrFormat{Line: i + 1, Msg: "line must start with space/-/+"}
			}
			switch t[0] {
			case ' ':
				goc++
				gnc++
			case '-':
				goc++
			case '+':
				gnc++
			default:
				return nil, &ErrFormat{Line: i + 1, Msg: "line must start with space/-/+"}
			}
			row := hunk.Row{Kind: uint8(t[0]), Text: contentOf(t[1:])}
			end := "\n"
			if !ls[i].nl {
				end = ""
			} else if strings.HasSuffix(t[1:], "\r") {
				end = "\r\n"
			}
			if i+1 < len(ls) && ls[i+1].text == noNL {
				end = ""
				if row.Kind != '+' {
					row.OldEnd = ""
				}
				if row.Kind != '-' {
					row.NewEnd = ""
				}
				i++
			} else if row.Kind != '+' {
				row.OldEnd = end
			}
			if row.Kind != '-' && end == "" {
				row.NewEnd = ""
			} else if row.Kind != '-' {
				row.NewEnd = end
			}
			h.Rows = append(h.Rows, row)
			i++
		}
		p.Hunks = append(p.Hunks, h)
		if lim.MaxHunks > 0 && len(p.Hunks) > lim.MaxHunks {
			return nil, &ErrFormat{Line: ln, Msg: "patch exceeds hunk limit"}
		}
	}
	return p, nil
}

func parseHdr(s string, ln int) (int, int, int, int, error) {
	end := strings.LastIndex(s, " @@")
	if !strings.HasPrefix(s, "@@ ") || end < 4 {
		return 0, 0, 0, 0, &ErrFormat{Line: ln, Msg: "bad hunk header"}
	}
	mid := s[4:end]
	parts := strings.Fields(mid)
	if len(parts) != 2 || parts[0][0] != '-' || parts[1][0] != '+' {
		return 0, 0, 0, 0, &ErrFormat{Line: ln, Msg: "bad hunk ranges"}
	}
	o, e1 := parseRange(parts[0][1:], ln)
	n, e2 := parseRange(parts[1][1:], ln)
	if e1 != nil || e2 != nil {
		return 0, 0, 0, 0, &ErrFormat{Line: ln, Msg: "bad range number"}
	}
	return o[0], o[1], n[0], n[1], nil
}

func parseRange(s string, ln int) ([2]int, error) {
	var r [2]int
	r[1] = 1
	if i := strings.IndexByte(s, ','); i >= 0 {
		v, err := strconv.Atoi(s[i+1:])
		if err != nil || v < 0 {
			return r, errors.New("x")
		}
		r[1] = v
		s = s[:i]
	}
	v, err := strconv.Atoi(s)
	if err || v < 0 {
		return r, errors.New("x")
	}
	r[0] = v
	return r, nil
}
