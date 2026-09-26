// Package esc 实现单个负载的转义与反转义。
// 转义字节为 0x5C（'\'）：负载中的 0x5C 写成 5C 5C，0x0A 写成 5C 6E。
package esc

import "errors"

// 哨兵错误，二者互不相同。
var (
	ErrInvalidEscape  = errors.New("esc: invalid escape (\\ followed by byte other than \\ or n)")
	ErrDanglingEscape = errors.New("esc: dangling escape (\\ at end of input)")
)

const (
	EscapeByte = 0x5C // '\'
	Newline    = 0x0A // '\n'
)

// Escape 返回 payload 的转义形式：'\' → `\\`，'\n' → `\n`，其余照抄。
func Escape(payload []byte) []byte {
	out := make([]byte, 0, len(payload))
	for _, b := range payload {
		switch b {
		case EscapeByte:
			out = append(out, EscapeByte, EscapeByte)
		case Newline:
			out = append(out, EscapeByte, 'n')
		default:
			out = append(out, b)
		}
	}
	return out
}

// Unescape 严格反转义：`\`→0x5C，`\n`→0x0A；`\` 后跟其他字节报
// ErrInvalidEscape，`\` 位于末尾报 ErrDanglingEscape。失败返回 (nil, error)。
func Unescape(b []byte) ([]byte, error) {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != EscapeByte {
			out = append(out, b[i])
			continue
		}
		if i+1 >= len(b) {
			return nil, ErrDanglingEscape
		}
		switch b[i+1] {
		case EscapeByte:
			out = append(out, EscapeByte)
		case 'n':
			out = append(out, Newline)
		default:
			return nil, ErrInvalidEscape
		}
		i++
	}
	return out, nil
}
