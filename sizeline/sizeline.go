// Package sizeline encodes and decodes chunk size lines:
// "<hex>[;k=v ...]\r\n" with optional quoted and escaped extension values.
package sizeline

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Ext is one chunk extension key/value pair.
type Ext struct {
	Key string
	Val string
}

// ErrSyntax reports a malformed size line.
var ErrSyntax = errors.New("sizeline: malformed size line")

func isTokenByte(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z':
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

func needsQuote(v string) bool {
	if v == "" {
		return true
	}
	for i := 0; i < len(v); i++ {
		if !isTokenByte(v[i]) {
			return true
		}
	}
	return false
}

// appendExts writes ";k=v" pairs, quoting and escaping values when needed.
func appendExts(dst []byte, exts []Ext) []byte {
	for _, e := range exts {
		dst = append(dst, ';')
		dst = append(dst, e.Key...)
		dst = append(dst, '=')
		if needsQuote(e.Val) {
			dst = append(dst, '"')
			for i := 0; i < len(e.Val); i++ {
				if e.Val[i] == '"' || e.Val[i] == '\\' {
					dst = append(dst, '\\')
				}
				dst = append(dst, e.Val[i])
			}
			dst = append(dst, '"')
		} else {
			dst = append(dst, e.Val...)
		}
	}
	return dst
}

// Encode returns the full size line for a chunk, including the trailing CRLF.
func Encode(size uint64, exts []Ext) []byte {
	dst := strconv.AppendUint(nil, size, 16)
	dst = appendExts(dst, exts)
	return append(dst, '\r', '\n')
}

// ExtsLen returns the encoded length of the extensions portion, ';'s included.
func ExtsLen(exts []Ext) int {
	return len(appendExts(nil, exts))
}

// Decode parses one size line without its trailing CRLF.
func Decode(line []byte) (uint64, []Ext, error) {
	i := 0
	for i < len(line) && line[i] != ';' {
		i++
	}
	if i == 0 || i > 16 {
		return 0, nil, ErrSyntax
	}
	size, err := strconv.ParseUint(string(line[:i]), 16, 64)
	if err != nil {
		return 0, nil, ErrSyntax
	}
	var exts []Ext
	for i < len(line) {
		i++ // consume ';'
		eq := i
		for eq < len(line) && line[eq] != '=' {
			eq++
		}
		if eq == i || eq == len(line) {
			return 0, nil, ErrSyntax
		}
		key := string(line[i:eq])
		i = eq + 1
		var val string
		if i < len(line) && line[i] == '"' {
			var sb strings.Builder
			i++
			for {
				if i >= len(line) {
					return 0, nil, ErrSyntax
				}
				c := line[i]
				if c == '"' {
					i++
					break
				}
				if c == '\\' {
					i++
					if i >= len(line) {
						return 0, nil, ErrSyntax
					}
					c = line[i]
				}
				sb.WriteByte(c)
				i++
			}
			val = sb.String()
		} else {
			j := i
			for j < len(line) && line[j] != ';' {
				j++
			}
			val = string(line[i:j])
			i = j
		}
		if i < len(line) && line[i] != ';' {
			return 0, nil, ErrSyntax
		}
		exts = append(exts, Ext{Key: key, Val: val})
	}
	return size, exts, nil
}

// String formats a size line for diagnostics, without the trailing CRLF.
func String(size uint64, exts []Ext) string {
	enc := Encode(size, exts)
	return fmt.Sprintf("%s", enc[:len(enc)-2])
}
