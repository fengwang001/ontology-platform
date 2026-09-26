// Package esc escapes and unescapes a single frame payload.
//
//	'\\' -> '\\' '\\'   (5C 5C)
//	'\n' -> '\\' 'n'    (5C 6E)
//
// Every other byte passes through unchanged. On unescape a backslash must be
// followed by '\\' or 'n'; a backslash with no following byte is dangling.
package esc

import "errors"

// Sentinel errors are distinct so callers can judge the failure kind.
var (
	// ErrIllegalEscape: a backslash followed by a byte other than '\\' or 'n'.
	ErrIllegalEscape = errors.New("esc: illegal escape sequence")
	// ErrDanglingEscape: a trailing backslash with nothing to escape.
	ErrDanglingEscape = errors.New("esc: dangling escape")
)

// Escape returns the escaped form of payload. The returned slice never aliases
// payload even when no escaping is needed.
func Escape(payload []byte) []byte {
	out := make([]byte, 0, len(payload))
	for _, b := range payload {
		switch b {
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, b)
		}
	}
	return out
}

// Unescape is the exact inverse of Escape. It returns nil and a sentinel
// error on any malformed input; no partially unescaped bytes are returned.
// A backslash with no payload byte to pair with — end of input or a bare
// frame delimiter (0x0A) — is dangling; any other following byte is illegal.
func Unescape(in []byte) ([]byte, error) {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		b := in[i]
		if b != '\\' {
			out = append(out, b)
			continue
		}
		i++
		if i >= len(in) {
			return nil, ErrDanglingEscape
		}
		switch in[i] {
		case '\\':
			out = append(out, '\\')
		case 'n':
			out = append(out, '\n')
		case '\n': // unpaired '\' right where a frame ends
			return nil, ErrDanglingEscape
		default:
			return nil, ErrIllegalEscape
		}
	}
	return out, nil
}
