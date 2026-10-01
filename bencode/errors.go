package bencode

import (
	"errors"
	"fmt"
)

// 错误原因（可用 errors.Is 判定）。
var (
	// ErrIllegalFirstByte 非法首字节：在期待值起始处出现无法开始任何值的字节。
	ErrIllegalFirstByte = errors.New("bencode: illegal first byte")
	// ErrIntLeadingZero 整数前导零。
	ErrIntLeadingZero = errors.New("bencode: integer leading zero")
	// ErrNegativeZero 负零 i-0e。
	ErrNegativeZero = errors.New("bencode: negative zero")
	// ErrIntOverflow 整数超出 int64 范围。
	ErrIntOverflow = errors.New("bencode: integer overflow")
	// ErrLenLeadingZero 字节串长度前导零。
	ErrLenLeadingZero = errors.New("bencode: string length leading zero")
	// ErrLenTooLarge 字节串长度超过 MaxString 上限。
	ErrLenTooLarge = errors.New("bencode: string length exceeds limit")
	// ErrDepthExceeded 嵌套深度超过上限 D。
	ErrDepthExceeded = errors.New("bencode: nesting depth exceeded")
	// ErrKeyNotString 字典键不是字节串。
	ErrKeyNotString = errors.New("bencode: dictionary key is not a byte string")
	// ErrKeyOrder 字典键未按原始字节序严格升序。
	ErrKeyOrder = errors.New("bencode: dictionary keys out of order")
	// ErrDuplicateKey 字典键重复。
	ErrDuplicateKey = errors.New("bencode: duplicate dictionary key")
	// ErrSyntax 语法错误（整数/长度中出现非法字节等）。
	ErrSyntax = errors.New("bencode: syntax error")
	// ErrPoisoned 解码器已因先前错误进入粘滞失败态。
	ErrPoisoned = errors.New("bencode: decoder poisoned by previous error")
)

// Error 描述一次规范性违规，携带首个违规字节的绝对偏移。
type Error struct {
	Kind   error // 上述某一原因，可用 errors.Is 判定
	Offset int64 // 首个违规字节在整个流中的绝对偏移（从 0 开始）
	Byte   byte  // 违规字节本身
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s at offset %d (byte 0x%02x)", e.Kind, e.Offset, e.Byte)
}

func (e *Error) Unwrap() error { return e.Kind }
