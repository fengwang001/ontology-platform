package runs

import (
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

const MaxInt = int(^uint(0) >> 1)

var ErrCountOverflow = errors.New("runs: count overflow")

var (
	ErrLeadingZero    = errors.New("runs: leading zero count")
	ErrInvalidEscape  = errors.New("runs: invalid escape")
	ErrTrailingEscape = errors.New("runs: trailing escape")
	ErrMissingSymbol  = errors.New("runs: missing symbol")
	ErrInvalidUTF8    = errors.New("runs: invalid UTF-8")
)

type ParseError struct {
	Offset int
	Kind   error
}

func (e *ParseError) Error() string { return e.Kind.Error() }
func (e *ParseError) Unwrap() error { return e.Kind }

type Run struct {
	Symbol rune
	Count  int
}

type SymbolFunc func(symbol rune, size, count, start int) error

type Scanner struct {
	yield                                  SymbolFunc
	examined, count, countStart, escapeStart, runeStart int
	haveCount, escaped                     bool
	pending                                [4]byte
	pendingLen                             int
	err                                    *ParseError
}

func NewScanner(yield SymbolFunc) *Scanner { return &Scanner{yield: yield} }

func (s *Scanner) Write(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	for i := 0; i < len(p); {
		global, b := s.examined+i, p[i]
		switch {
		case s.escaped:
			i, s.escaped = s.escapedByte(p, i)
		case b == '\\':
			s.escaped, s.escapeStart = true, global
		case '0' <= b && b <= '9':
			i = s.digit(p, i, global)
		case b < 0x80:
			i = s.ascii(p, i)
		default:
			i = s.rune(p, i, global)
		}
		if s.err != nil {
			return i, s.err
		}
	}
	s.examined += len(p)
	return len(p), nil
}

func (s *Scanner) Close() error {
	if s.err != nil {
		return s.err
	}
	switch {
	case s.escaped:
		return s.fail(ErrTrailingEscape, s.escapeStart)
	case s.pendingLen > 0:
		return s.fail(ErrInvalidUTF8, s.runeStart)
	case s.haveCount:
		return s.fail(ErrMissingSymbol, s.countStart)
	}
	return nil
}

func (s *Scanner) Examined() int { return s.examined }

func (s *Scanner) emit(symbol rune, size, start int) error {
	count := 1
	if s.haveCount {
		count, s.haveCount, s.count = s.count, false, 0
	}
	return s.yield(symbol, size, count, start)
}

func (s *Scanner) takeRune(rest []byte, offset int) (rune, int, int, int, bool) {
	start := offset - s.pendingLen
	for n := 1; n <= len(rest); n++ {
		s.pending[s.pendingLen] = rest[n-1]
		s.pendingLen++
		if !utf8.FullRune(s.pending[:s.pendingLen]) {
			continue
		}
		r, size := utf8.DecodeRune(s.pending[:s.pendingLen])
		s.pendingLen = 0
		return r, size, n, start, true
	}
	s.runeStart = start
	return utf8.RuneError, 1, len(rest), start, false
}

func (s *Scanner) fail(kind error, offset int) error {
	return &ParseError{Offset: offset, Kind: kind}
}

func Split(s string) []Run {
	if s == "" {
		return nil
	}
	var result []Run
	for _, symbol := range s {
		if len(result) > 0 && result[len(result)-1].Symbol == symbol {
			result[len(result)-1].Count++
			continue
		}
		result = append(result, Run{Symbol: symbol, Count: 1})
	}
	return result
}

func AddDigit(count int, digit byte) (int, error) {
	if digit < '0' || digit > '9' {
		return 0, strconv.ErrSyntax
	}
	value := int(digit - '0')
	if count > (MaxInt-value)/10 {
		return 0, ErrCountOverflow
	}
	return count*10 + value, nil
}

func FormatCount(count int) string {
	return strconv.Itoa(count)
}

func WriteRepeated(w io.Writer, symbol rune, count int) error {
	var unit, block [4]byte
	size := utf8.EncodeRune(unit[:], symbol)
	for i := 0; i+size <= len(block); i += size {
		copy(block[i:i+size], unit[:size])
	}
	perBlock := len(block) / size
	for count > 0 {
		take := min(count, perBlock)
		if _, err := w.Write(block[:take*size]); err != nil {
			return err
		}
		count -= take
	}
	return nil
}

func Fits(count, size, limit int) bool { return count <= (limit)/size }
