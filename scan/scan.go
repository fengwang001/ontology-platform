package scan

import (
	"errors"
	"strings"
	"sync/atomic"
	"unicode"
)

var (
	ErrEmpty         = errors.New("identifier is empty")
	ErrInvalidRune   = errors.New("identifier contains an invalid rune")
	ErrInvalidShape  = errors.New("identifier has invalid underscore or leading digit shape")
)

type Token struct {
	Text     string
	Acronym  bool
}

type Scanner struct {
	reads atomic.Int64
}

func NewScanner() *Scanner { return &Scanner{} }

func (s *Scanner) Reads() int64 { return s.reads.Load() }

func (s *Scanner) Split(src string) ([]Token, error) {
	s.reads.Store(0)
	if src == "" {
		return nil, ErrEmpty
	}
	bounds := make([]bool, len(src)+1)
	prevUnderscore := true
	for i, r := range src {
		s.reads.Add(1)
		if r > unicode.MaxASCII || !(r == '_' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			return nil, ErrInvalidRune
		}
		if i == 0 && r >= '0' && r <= '9' {
			return nil, ErrInvalidShape
		}
		if r == '_' {
			if prevUnderscore {
				return nil, ErrInvalidShape
			}
			prevUnderscore = true
			bounds[i] = true
			continue
		}
		prevUnderscore = false
		if i > 0 && src[i-1] == '_' {
			bounds[i] = true
			continue
		}
		if i > 0 {
			if at, ok := classify(i, src); ok {
				bounds[at] = true
			}
		}
	}
	if prevUnderscore || unicode.IsDigit(rune(src[0])) {
		return nil, ErrInvalidShape
	}

	var out []Token
	start := 0
	for i := 0; i <= len(src); i++ {
		if i == len(src) || bounds[i] {
			out = appendToken(out, src, start, i)
			if i < len(src) && src[i] == '_' {
				i++
			}
			start = i
		}
	}
	s.reads.Add(int64(len(src)))
	return out, nil
}

func appendToken(out []Token, src string, start, end int) []Token {
	if start >= end {
		return out
	}
	part := src[start:end]
	return append(out, Token{Text: strings.ToLower(part), Acronym: isUpper(part)})
}

func classify(i int, src string) (int, bool) {
	upper := func(b byte) bool { return b >= 'A' && b <= 'Z' }
	lower := func(b byte) bool { return b >= 'a' && b <= 'z' }
	digit := func(b byte) bool { return b >= '0' && b <= '9' }
	letter := func(b byte) bool { return upper(b) || lower(b) }
	prev, cur := src[i-1], src[i]
	if letter(prev) && digit(cur) || digit(prev) && letter(cur) || lower(prev) && upper(cur) {
		return i, true
	}
	if upper(prev) && lower(cur) && i >= 3 && upper(src[i-2]) && upper(src[i-3]) {
		return i - 1, true
	}
	if upper(prev) && lower(cur) {
		if i >= 2 && upper(src[i-2]) {
			return i, true
		}
	}
	return 0, false
}

func isUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		if !(s[i] >= 'A' && s[i] <= 'Z') {
			return false
		}
	}
	return s != ""
}
