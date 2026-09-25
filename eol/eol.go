// Package eol 识别行尾：\r\n、单独 \r、\n，以及切分点处待定的 \r。
package eol

import "errors"

const (
	CR = '\r'
	LF = '\n'
)

// ErrPendingCR 表示切分点恰好落在 \r 之后：是否为 CRLF 取决于下一字节。
var ErrPendingCR = errors.New("eol: pending CR at boundary")

// MatchAt 判断 buf[pos:] 开头是否为行尾。
// 返回规范化后的行尾（恒为 \n）、消费字节数与状态：
// n=1 且 err==ErrPendingCR 表示此处是待定的 \r。
func MatchAt(buf []byte, pos int) (norm byte, n int, err error) {
	if pos >= len(buf) {
		return 0, 0, nil
	}
	switch buf[pos] {
	case LF:
		return LF, 1, nil
	case CR:
		if pos+1 < len(buf) {
			if buf[pos+1] == LF {
				return LF, 2, nil
			}
			return LF, 1, nil
		}
		return LF, 1, ErrPendingCR
	}
	return 0, 0, nil
}

// JoinsCR 报告待定 \r 与下一字节是否构成 CRLF。
func JoinsCR(next byte) bool { return next == LF }

// IsLineEnd 报告字节是否可能是行尾首字节。
func IsLineEnd(b byte) bool { return b == CR || b == LF }
