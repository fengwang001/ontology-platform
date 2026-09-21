package editdist

import (
	"errors"
	"fmt"
)

// ErrNegativeK 表示调用方传入了负数的阈值 k。
var ErrNegativeK = errors.New("editdist: k must be non-negative")

// UTF8Error 表示某个输入串含有非法 UTF-8 字节序列。
// Side 指出是哪一侧（"a" 或 "b"，RuneLen 中为 "input"），
// Offset 是出错位置的字节偏移（0 起）。
type UTF8Error struct {
	Side   string
	Offset int
}

func (e *UTF8Error) Error() string {
	return fmt.Sprintf("editdist: invalid UTF-8 in argument %q at byte offset %d", e.Side, e.Offset)
}
