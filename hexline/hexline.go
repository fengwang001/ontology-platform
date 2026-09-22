// Package hexline parses chunk-size lines: a hexadecimal size
// followed by optional chunk extensions (";name=value").
//
// The package is purely about the size line; it has no knowledge of
// chunk data, trailers, or overall message state, and depends on no
// other package in this module.
package hexline

import "errors"

var (
	// ErrNotHex indicates the size field is empty or contains a
	// non-hexadecimal character.
	ErrNotHex = errors.New("hexline: invalid hexadecimal chunk size")
	// ErrSizeOverflow indicates the size does not fit in a uint64.
	ErrSizeOverflow = errors.New("hexline: chunk size overflows uint64")
	// ErrUnterminatedQuote indicates a quoted-string extension value
	// was not closed before the end of the line.
	ErrUnterminatedQuote = errors.New("hexline: unterminated quoted-string in chunk extension")
)

// Parse parses a complete chunk-size line (without the trailing CRLF)
// and returns the declared chunk size.
//
// The size is hexadecimal, case-insensitive, and may carry leading
// zeros. Zero or more chunk extensions (";name=value" or
// ;name="quoted value") may follow; they are skipped, not interpreted.
// Inside a quoted value, ';' and '=' are ordinary characters and \"
// is an escaped quote, not the end of the string.
func Parse(line []byte) (uint64, error) {
	i := 0
	var size uint64
	digits := 0
	for i < len(line) && isHex(line[i]) {
		if size >= 1<<60 {
			return 0, ErrSizeOverflow
		}
		size = size*16 + uint64(hexVal(line[i]))
		i++
		digits++
	}
	if digits == 0 {
		return 0, ErrNotHex
	}
	for i < len(line) {
		if line[i] != ';' {
			return 0, ErrNotHex
		}
		i++ // consume ';'
		var err error
		i, err = skipExtension(line, i)
		if err != nil {
			return 0, err
		}
	}
	return size, nil
}

// skipExtension skips one extension (name and optional value) starting
// just after the ';'. It returns the index of the next ';' or len(line).
func skipExtension(line []byte, i int) (int, error) {
	for i < len(line) && line[i] != '=' && line[i] != ';' {
		i++
	}
	if i >= len(line) || line[i] == ';' {
		return i, nil
	}
	i++ // consume '='
	if i < len(line) && line[i] == '"' {
		return skipQuoted(line, i+1)
	}
	for i < len(line) && line[i] != ';' {
		i++
	}
	return i, nil
}

// skipQuoted skips a quoted-string body starting just after the
// opening quote. A backslash escapes the next byte.
func skipQuoted(line []byte, i int) (int, error) {
	for i < len(line) {
		switch line[i] {
		case '\\':
			if i+1 < len(line) {
				i += 2
				continue
			}
			return 0, ErrUnterminatedQuote
		case '"':
			return i + 1, nil
		}
		i++
	}
	return 0, ErrUnterminatedQuote
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func hexVal(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	default:
		return int(b-'A') + 10
	}
}
