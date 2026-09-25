// Package udiff 渲染与解析统一格式（unified diff）文本。
package udiff

import (
	"ontology/hunk"
	"ontology/lines"
	"strconv"
	"strings"
)

type Kind = hunk.Kind

const (
	Context = hunk.Context
	Removed = hunk.Removed
	Added   = hunk.Added
)

// Entry 是一条正文行；NoNL 表示其对应源行无行尾。
type Entry struct {
	Kind Kind
	Line lines.Line
	NoNL bool
}

// Hunk 是解析或待渲染的一个 hunk。
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Body               []Entry
}

// Patch 是一份单文件补丁；本实现文件头恒为空标签。
type Patch struct{ Hunks []Hunk }

// FormatError 是补丁文本格式错误，带补丁文本行号。
type FormatError struct {
	Line int
	Msg  string
}

func (e *FormatError) Error() string {
	return "udiff: format error at line " + strconv.Itoa(e.Line) + ": " + e.Msg
}

const noNL = `\ No newline at end of file`

// Render 渲染为统一格式字节串。
func Render(p Patch) []byte {
	var w []byte
	if len(p.Hunks) > 0 {
		w = append(w, "--- \n+++ \n"...)
	}
	for _, h := range p.Hunks {
		w = append(w, "@@"...)
		w = append(w, renderRange('-', h.OldStart, h.OldCount)...)
		w = append(w, renderRange('+', h.NewStart, h.NewCount)...)
		w = append(w, " @@\n"...)
		for _, e := range h.Body {
			sign := byte(' ')
			if e.Kind == Removed {
				sign = '-'
			} else if e.Kind == Added {
				sign = '+'
			}
			w = append(w, sign)
			w = append(w, e.Line.Data...)
			if e.NoNL {
				w = append(w, '\n')
				w = append(w, noNL...)
				w = append(w, '\n')
			} else {
				w = append(w, e.Line.NL...)
			}
		}
	}
	return w
}

func renderRange(sign byte, start, count int) []byte {
	s := " " + string(sign) + strconv.Itoa(start)
	if count != 1 {
		s += "," + strconv.Itoa(count)
	}
	return []byte(s)
}

type tok struct {
	text   string
	rawNL  string
	term   bool
	lineNo int
}

func tokenize(b []byte) []tok {
	var ts []tok
	start, no := 0, 1
	for i := 0; i <= len(b); i++ {
		if i < len(b) && b[i] != '\n' {
			continue
		}
		textEnd, nl := i, ""
		if i < len(b) {
			if i > start && b[i-1] == '\r' {
				nl = "\r\n"
				textEnd = i - 1
			} else {
				nl = "\n"
			}
		}
		ts = append(ts, tok{string(b[start:textEnd]), nl, i < len(b), no})
		no++
		start = i + 1
	}
	if len(b) > 0 && b[len(b)-1] == '\n' {
		ts = ts[:len(ts)-1]
	}
	return ts
}

// Parse 严格解析；行数不符、非法前缀均返回 *FormatError。
func Parse(b []byte) (Patch, error) {
	ts := tokenize(b)
	i := 0
	if len(ts) >= 2 && ts[0].text == "--- " && ts[1].text == "+++ " {
		i = 2
	} else if len(ts) >= 1 && (ts[0].text == "--- " || strings.HasPrefix(ts[0].text, "@@")) {
		if ts[0].text == "--- " {
			return Patch{}, &FormatError{ts[0].lineNo, "missing +++ header"}
		}
	}
	var p Patch
	for i < len(ts) {
		if !strings.HasPrefix(ts[i].text, "@@ ") {
			return Patch{}, &FormatError{ts[i].lineNo, "expected hunk header"}
		}
		h, err := parseHeader(ts[i])
		if err != nil {
			return Patch{}, err
		}
		i++
		oc, nc := h.OldCount, h.NewCount
		for oc+nc > 0 {
			if i >= len(ts) {
				return Patch{}, &FormatError{lastLine(ts), "truncated hunk body"}
			}
			t := ts[i]
			if len(t.text) == 0 {
				return Patch{}, &FormatError{t.lineNo, "bad line prefix"}
			}
			var e Entry
			switch t.text[0] {
			case ' ':
				e.Kind, e.Line.Data = Context, []byte(t.text[1:])
				oc--
				nc--
			case '-':
				e.Kind, e.Line.Data = Removed, []byte(t.text[1:])
				oc--
			case '+':
				e.Kind, e.Line.Data = Added, []byte(t.text[1:])
				nc--
			default:
				return Patch{}, &FormatError{t.lineNo, "bad line prefix"}
			}
			i++
			if !t.term {
				e.NoNL = true
			} else if i < len(ts) && ts[i].text == noNL {
				e.NoNL = true
				i++
			}
			if e.NoNL {
				e.Line.NL = nil
			} else {
				e.Line.NL = []byte(t.rawNL)
			}
			h.Body = append(h.Body, e)
		}
		p.Hunks = append(p.Hunks, h)
	}
	return p, nil
}

func lastLine(ts []tok) int {
	if len(ts) == 0 {
		return 0
	}
	return ts[len(ts)-1].lineNo
}

func parseHeader(t tok) (Hunk, error) {
	s := t.text
	if !strings.HasSuffix(s, " @@") {
		return Hunk{}, &FormatError{t.lineNo, "bad hunk header"}
	}
	fields := strings.Split(s, " ")
	if len(fields) != 4 || fields[0] != "@@" || fields[3] != "@@" {
		return Hunk{}, &FormatError{t.lineNo, "bad hunk header"}
	}
	o, err := parseOne(fields[1], '-', t.lineNo)
	if err != nil {
		return Hunk{}, err
	}
	n, err := parseOne(fields[2], '+', t.lineNo)
	if err != nil {
		return Hunk{}, err
	}
	return Hunk{OldStart: o[0], OldCount: o[1], NewStart: n[0], NewCount: n[1]}, nil
}

func parseOne(f string, sign byte, ln int) ([2]int, error) {
	if len(f) < 2 || f[0] != sign {
		return [2]int{}, &FormatError{ln, "bad range"}
	}
	num := f[1:]
	count := 1
	if i := strings.IndexByte(num, ','); i >= 0 {
		c, err := strconv.Atoi(num[i+1:])
		if err != nil {
			return [2]int{}, &FormatError{ln, "bad count"}
		}
		count = c
		num = num[:i]
	}
	start, err := strconv.Atoi(num)
	if err != nil {
		return [2]int{}, &FormatError{ln, "bad start"}
	}
	return [2]int{start, count}, nil
}
