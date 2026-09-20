package sortkey

import (
	"errors"
	"fmt"
)

// ErrNeedsRebalance 表示新键长度将超过声明的上限，需要重排。
// 它与参数错误是不同的类别：调用方应捕获它并触发
// Sequence.Rebalance，而不是视为输入非法。
var ErrNeedsRebalance = errors.New("sortkey: key length limit reached, rebalance required")

// ErrInvalidOrder 表示左键不小于右键的参数错误。
var ErrInvalidOrder = errors.New("sortkey: left key must be strictly less than right key")

// InvalidKeyError 表示键中出现了字符集之外的字符。
// 它指出具体是哪个字符、出现在哪个位置。
type InvalidKeyError struct {
	Key  string // 被检查的键
	Pos  int    // 非法字符的字节位置
	Char byte   // 非法字符本身
}

// Error 实现 error 接口。
func (e *InvalidKeyError) Error() string {
	return fmt.Sprintf("sortkey: key %q contains disallowed character %q at byte %d", e.Key, e.Char, e.Pos)
}
