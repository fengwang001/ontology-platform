package qpline

import "strings"

const (
	MaxLine  = 76
	hexUpper = "0123456789ABCDEF"
)

type Writer struct {
	b       strings.Builder
	pending []byte
	checked int64
}

func NewWriter() *Writer { return &Writer{} }

func (w *Writer) Write(p []byte) {
	w.checked += int64(len(p))
	if len(w.pending) > 0 {
		p = append(append([]byte{}, w.pending...), p...)
		w.pending = w.pending[:0]
	}
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '\n' {
			end := i
			if end > start && p[end-1] == '\r' {
				end--
			}
			if end > start {
				w.encodeLine(p[start:end])
			}
			w.b.WriteString("\r\n")
			start = i + 1
		}
	}
	if start < len(p) {
		w.pending = append(w.pending, p[start:]...)
	}
	if len(w.pending) > 0 && w.pending[len(w.pending)-1] == '\r' {
		line := w.pending[:len(w.pending)-1]
		w.encodeLine(line)
		w.pending = append(w.pending[:0], '\r')
	}
}

func (w *Writer) Close() {
	if len(w.pending) > 0 {
		if w.pending[len(w.pending)-1] == '\r' {
			line := w.pending[:len(w.pending)-1]
			w.encodeLine(line)
			w.emit('\r', true)
		} else {
			w.encodeLine(w.pending)
		}
		w.pending = nil
	}
}

func (w *Writer) Bytes() []byte { return []byte(w.b.String()) }

func (w *Writer) Checked() int64 { return w.checked }

func (w *Writer) encodeLine(line []byte) {
	widths := make([]int, len(line))
	for i, b := range line {
		if isLiteral(b) {
			widths[i] = 1
		} else {
			widths[i] = 3
		}
	}
	trailingFrom := len(line)
	for trailingFrom > 0 {
		b := line[trailingFrom-1]
		if b != ' ' && b != '\t' {
			break
		}
		trailingFrom--
	}
	col := 0
	for i, b := range line {
		trailing := i >= trailingFrom
		width := 1
		if trailing && (b == ' ' || b == '\t') {
			width = 3
		} else {
			width = widths[i]
		}
		if col > 0 && (col+width > MaxLine || col+width == MaxLine && i < len(line)-1) {
			w.b.WriteString("=\r\n")
			col = 0
		}
		if width == 3 {
			w.emit(b, true)
		} else {
			w.b.WriteByte(b)
		}
		col += width
	}
}

func isLiteral(b byte) bool {
	return b >= 33 && b <= 126 && b != '=' || b == ' ' || b == '\t'
}

func (w *Writer) emit(b byte, encoded bool) {
	if !encoded {
		w.b.WriteByte(b)
		return
	}
	w.b.WriteByte('=')
	w.b.WriteByte(hexUpper[b>>4])
	w.b.WriteByte(hexUpper[b&0x0f])
}
