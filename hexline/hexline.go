// Package hexline parses the chunk-size line of chunked transfer-coding:
// a hexadecimal size optionally followed by chunk extensions of the
// form ";name=value" or ";name=\"quoted value\"". It has no dependencies.
package hexline

import (
	"errors"
	"fmt"
	"math"
)

// Sentinels for the failures Parse can report; use errors.Is to match.
var (
	ErrBadSize       = errors.New("hexline: invalid chunk size")
	ErrUnclosedQuote = errors.New("hexline: unterminated quoted-string in chunk extension")
	ErrSizeOverflow  = errors.New("hexline: chunk size overflows uint64")
)

// ParseError describes a malformed chunk-size line.
type ParseError struct {
	Err   error // one of the package sentinels
	Index int   // offset of the offending byte within the line
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%v (line offset %d)", e.Err, e.Index)
}

// Unwrap returns the sentinel, so errors.Is(err, ErrBadSize) works.
func (e *ParseError) Unwrap() error { return e.Err }

// Parse parses a complete chunk-size line (without the trailing CRLF)
// and returns the declared size. The size is hexadecimal, case
// insensitive, and may carry leading zeros. Chunk extensions are
// validated for balanced quoting but otherwise ignored: ';' and '='
// inside a quoted-string are data, and \" is an escaped quote, not
// the end of the string.
func Parse(line []byte) (uint64, *ParseError) {
	size, i, perr := parseSize(line)
	if perr != nil {
		return 0, perr
	}
	for i < len(line) {
		if line[i] != ';' {
			return 0, &ParseError{Err: ErrBadSize, Index: i}
		}
		i, perr = skipExt(line, i+1)
		if perr != nil {
			return 0, perr
		}
	}
	return size, nil
}

// parseSize consumes the leading hexadecimal size and returns the
// index of the first byte after it.
func parseSize(line []byte) (uint64, int, *ParseError) {
	var size uint64
	i := 0
	for i < len(line) {
		v, ok := hexVal(line[i])
		if !ok {
			break
		}
		if size > (math.MaxUint64-15)/16 {
			return 0, 0, &ParseError{Err: ErrSizeOverflow, Index: i}
		}
		size = size*16 + v
		i++
	}
	if i == 0 {
		return 0, 0, &ParseError{Err: ErrBadSize, Index: 0}
	}
	return size, i, nil
}

// skipExt skips one chunk extension, starting just after its ';',
// and returns the index of the next ';' or len(line).
func skipExt(line []byte, i int) (int, *ParseError) {
	for i < len(line) && line[i] != '=' && line[i] != ';' {
		i++
	}
	if i == len(line) || line[i] == ';' {
		return i, nil
	}
	i++ // skip '='
	if i < len(line) && line[i] == '"' {
		return skipQuoted(line, i)
	}
	for i < len(line) && line[i] != ';' {
		i++
	}
	return i, nil
}

// skipQuoted skips a quoted-string whose opening quote is at index
// quote. A backslash escapes the following byte, so \" does not end
// the string. Returns the index just past the closing quote.
func skipQuoted(line []byte, quote int) (int, *ParseError) {
	i := quote + 1
	for i < len(line) {
		switch line[i] {
		case '\\':
			if i+1 < len(line) {
				i += 2
				continue
			}
			i++
		case '"':
			return i + 1, nil
		default:
			i++
		}
	}
	return i, &ParseError{Err: ErrUnclosedQuote, Index: quote}
}

func hexVal(b byte) (uint64, bool) {
	switch {
	case '0' <= b && b <= '9':
		return uint64(b - '0'), true
	case 'a' <= b && b <= 'f':
		return uint64(b-'a') + 10, true
	case 'A' <= b && b <= 'F':
		return uint64(b-'A') + 10, true
	}
	return 0, false
}
