// Package words 在 lex 状态机之上提供 POSIX shell 风格的词切分、
// 流式喂入以及重新引用（quote）能力。不做变量展开、通配或命令替换。
package words

import (
	"errors"

	"ontology/lex"
)

var (
	// ErrUnclosedSingle 表示单引号未闭合。
	ErrUnclosedSingle = errors.New("words: unclosed single quote")
	// ErrUnclosedDouble 表示双引号未闭合。
	ErrUnclosedDouble = errors.New("words: unclosed double quote")
	// ErrTrailingBackslash 表示输入以普通态的单个反斜杠结尾。
	ErrTrailingBackslash = errors.New("words: trailing backslash")
)

// SyntaxError 携带可判定的哨兵错误与起始字节偏移。
type SyntaxError struct {
	Err    error
	Offset int
}

func (e *SyntaxError) Error() string { return e.Err.Error() }
func (e *SyntaxError) Unwrap() error { return e.Err }

// Streamer 支持按任意字节切分流式喂入，结果与一次性 Split 完全一致。
type Streamer struct {
	m       lex.Machine
	cur     []byte
	started bool
	out     []string
	err     *SyntaxError
	closed  bool
}

// NewStreamer 创建一个空的流式分词器。
func NewStreamer() *Streamer { return &Streamer{} }

func (s *Streamer) emit(e lex.Event) {
	if e.Mark && !s.started {
		s.started = true
	}
	if len(e.Keep) > 0 {
		s.cur = append(s.cur, e.Keep...)
		s.started = true
	}
	switch e.Op {
	case lex.OpLit:
		s.cur = append(s.cur, e.Ch)
		s.started = true
	case lex.OpSep:
		if s.started {
			s.out = append(s.out, string(s.cur))
			s.cur = nil
			s.started = false
		}
	}
}

// Feed 喂入一段字节。Close 之后的喂入被忽略。错误只在 Close 时给出。
func (s *Streamer) Feed(p []byte) {
	if s.closed {
		return
	}
	for i := 0; i < len(p); i++ {
		s.emit(s.m.Step(p[i]))
	}
}

// Close 收尾：收束最后一个词并报告未闭合引号 / 结尾反斜杠。
func (s *Streamer) Close() error {
	if s.closed {
		return s.err
	}
	s.closed = true
	if s.started {
		s.out = append(s.out, string(s.cur))
		s.cur = nil
		s.started = false
	}
	status, off := s.m.Finish()
	switch status {
	case lex.StatusOpenSingle:
		s.err = &SyntaxError{Err: ErrUnclosedSingle, Offset: off}
	case lex.StatusOpenDouble:
		s.err = &SyntaxError{Err: ErrUnclosedDouble, Offset: off}
	case lex.StatusTrailingBS:
		s.err = &SyntaxError{Err: ErrTrailingBackslash, Offset: off}
	}
	return s.err
}

// Words 返回目前已收束的词。错误时返回出错前完整收束的词。
func (s *Streamer) Words() []string {
	if s.closed && s.err != nil {
		return append([]string(nil), s.out...)
	}
	n := len(s.out)
	if s.started {
		n++
	}
	w := make([]string, 0, n)
	w = append(w, s.out...)
	if s.started {
		w = append(w, string(s.cur))
	}
	return w
}

// Split 一次性切分整行。出错时返回出错前已收束的词与 *SyntaxError。
func Split(s string) ([]string, error) {
	st := NewStreamer()
	st.Feed([]byte(s))
	err := st.Close()
	return st.Words(), err
}

// Quote 把词列表重新引用成一行，保证 Split(Quote(w)) 与 w 逐词相等。
func Quote(w []string) string {
	parts := make([]string, len(w))
	for i, word := range w {
		if safeWord(word) {
			parts[i] = word
			continue
		}
		// 词含单引号时写成 '\''：先关闭单引号段，再用 \' 放一个字面单引号，
		// 再重新打开单引号段——因为单引号内无法转义单引号。
		parts[i] = "'" + replaceAll(word, "'", `'\''`) + "'"
	}
	return joinSpace(parts)
}

func safeWord(word string) bool {
	if word == "" {
		return false
	}
	for i := 0; i < len(word); i++ {
		c := word[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c >= 0x80: // 非 ASCII 字节在无引号下为普通字符，原样安全
		default:
			return false
		}
	}
	return true
}

func replaceAll(s, old, repl string) string {
	out := make([]byte, 0, len(s))
	for {
		i := indexByte(s, old[0])
		if i < 0 {
			out = append(out, s...)
			return string(out)
		}
		out = append(out, s[:i]...)
		out = append(out, repl...)
		s = s[i+1:]
	}
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func joinSpace(parts []string) string {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	if len(parts) > 1 {
		n += len(parts) - 1
	}
	out := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, p...)
	}
	return string(out)
}
