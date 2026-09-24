package logical

import (
	"bufio"
	"io"
	"unicode"
)

const (
	Blank = iota
	Comment
	Data
)

type Fragment struct {
	Text   string
	Line   int
	Column int
}

type Line struct {
	Type      int
	Fragments []Fragment
	StartLine int
}

type Reader struct {
	r       *bufio.Reader
	line    int
	checked int64
	pending []Fragment
	start   int
}

func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReader(r), start: 1}
}

func (r *Reader) CheckedBytes() int64 { return r.checked }

func (r *Reader) Next() (*Line, error) {
	for {
		text, err := r.readPhysical()
		if err == io.EOF && len(r.pending) == 0 && text == "" {
			return nil, io.EOF
		}

		kind := Blank
		if text != "" {
			kind = classify(text)
		}
		continued := kind == Data && oddTrailingBackslashes(text)
		if continued {
			text = text[:len(text)-1]
		}

		appendLine := len(r.pending) > 0
		if kind == Data {
			start := 1
			if appendLine {
				start += leadingSpaces(text)
				text = text[leadingSpaces(text):]
			}
			r.pending = append(r.pending, Fragment{Text: text, Line: r.line, Column: start})
		}

		if err != nil || kind != Data || !continued {
			line := &Line{Type: kind, Fragments: r.pending, StartLine: r.start}
			r.pending = nil
			r.start = r.line + 1
			if kind == Data {
				return line, nil
			}
			if err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
	}
}

func (r *Reader) readPhysical() (string, error) {
	var buf []byte
	r.line++
	for {
		b, err := r.r.ReadByte()
		if err != nil {
			r.checked += int64(len(buf))
			if err == io.EOF && r.line == 1 && len(buf) == 0 && r.start == 1 {
				r.line--
			}
			return string(buf), err
		}
		r.checked++
		if b == '\n' {
			return string(buf), nil
		}
		if b == '\r' {
			if next, peekErr := r.r.Peek(1); peekErr == nil && len(next) == 1 && next[0] == '\n' {
				_, _ = r.r.ReadByte()
				r.checked++
			}
			return string(buf), nil
		}
		buf = append(buf, b)
	}
}

func classify(s string) int {
	i := leadingSpaces(s)
	if i == len(s) {
		return Blank
	}
	if s[i] == '#' || s[i] == '!' {
		return Comment
	}
	return Data
}

func leadingSpaces(s string) int {
	i := 0
	for i < len(s) {
		r, size := unicode.DecodeRuneInString(s[i:])
		if !unicode.IsSpace(r) {
			return i
		}
		i += size
	}
	return i
}

func oddTrailingBackslashes(s string) bool {
	n := 0
	for n < len(s) && s[len(s)-1-n] == '\\' {
		n++
	}
	return n%2 == 1
}
