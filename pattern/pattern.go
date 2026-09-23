package pattern

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

var (
	ErrInvalidUTF8   = errors.New("invalid UTF-8")
	ErrInvalidEscape = errors.New("invalid escape")
	ErrTrailingEscape = errors.New("trailing escape")
)

type Side string

const (
	SidePattern Side = "pattern"
	SideText    Side = "text"
)

type Kind int

const (
	Any Kind = iota
	One
	Literal
)

type Token struct {
	Kind Kind
	Rune rune
}

type InvalidUTF8Error struct {
	Side       Side
	ByteOffset int
}

func (e InvalidUTF8Error) Error() string {
	return fmt.Sprintf("%s has invalid UTF-8 at byte %d", e.Side, e.ByteOffset)
}

func (e InvalidUTF8Error) Is(target error) bool {
	return target == ErrInvalidUTF8
}

type InvalidEscapeError struct {
	ByteOffset int
}

func (e InvalidEscapeError) Error() string {
	return fmt.Sprintf("invalid escape at byte %d", e.ByteOffset)
}

func (e InvalidEscapeError) Is(target error) bool {
	return target == ErrInvalidEscape
}

type TrailingEscapeError struct {
	ByteOffset int
}

func (e TrailingEscapeError) Error() string {
	return fmt.Sprintf("trailing escape at byte %d", e.ByteOffset)
}

func (e TrailingEscapeError) Is(target error) bool {
	return target == ErrTrailingEscape
}

type Pattern struct {
	tokens []Token
}

func Parse(raw string) (*Pattern, error) {
	if offset := invalidUTF8(raw); offset >= 0 {
		return nil, InvalidUTF8Error{Side: SidePattern, ByteOffset: offset}
	}

	tokens := make([]Token, 0, len(raw))
	for offset := 0; offset < len(raw); {
		r, size := utf8.DecodeRuneInString(raw[offset:])
		next := offset + size

		switch {
		case r == '\\':
			if next == len(raw) {
				return nil, TrailingEscapeError{ByteOffset: offset}
			}
			escaped, escapedSize := utf8.DecodeRuneInString(raw[next:])
			if escaped != '%' && escaped != '_' && escaped != '\\' {
				return nil, InvalidEscapeError{ByteOffset: offset}
			}
			tokens = append(tokens, Token{Kind: Literal, Rune: escaped})
			offset = next + escapedSize
		case r == '%':
			if len(tokens) == 0 || tokens[len(tokens)-1].Kind != Any {
				tokens = append(tokens, Token{Kind: Any})
			}
			offset = next
		case r == '_':
			tokens = append(tokens, Token{Kind: One})
			offset = next
		default:
			tokens = append(tokens, Token{Kind: Literal, Rune: r})
			offset = next
		}
	}

	return &Pattern{tokens: tokens}, nil
}

func (p *Pattern) NormalizedLength() int {
	return len(p.tokens)
}

func (p *Pattern) Tokens() []Token {
	tokens := make([]Token, len(p.tokens))
	copy(tokens, p.tokens)
	return tokens
}

func invalidUTF8(value string) int {
	for offset := 0; offset < len(value); {
		r, size := utf8.DecodeRuneInString(value[offset:])
		if r == utf8.RuneError && size == 1 {
			return offset
		}
		offset += size
	}
	return -1
}
