package lzw

import "fmt"

var (
	// ErrNotCleared 首个码不是清除码。
	ErrNotCleared = fmt.Errorf("lzw: first code is not clear code")
	// ErrCodeAfterClear 清除码之后的首个码不小于 256。
	ErrCodeAfterClear = fmt.Errorf("lzw: first code after clear is not a literal")
	// ErrCodeOutOfRange 码值大于解码端下一个待新增编号。
	ErrCodeOutOfRange = fmt.Errorf("lzw: code out of range")
	// ErrPaddingNonZero 结束码之后的补齐位非零。
	ErrPaddingNonZero = fmt.Errorf("lzw: non-zero padding after end code")
	// ErrTrailingData 结束码之后还有多余字节。
	ErrTrailingData = fmt.Errorf("lzw: trailing data after end code")
	// ErrTruncated 输入在结束码前被截断。
	ErrTruncated = fmt.Errorf("lzw: truncated before end code")
	// ErrClosed 编码器 Close 之后再 Write，或重复 Close。
	ErrClosed = fmt.Errorf("lzw: encoder already closed")
)

// CodeError 携带出错码的序号（从 0 开始，含清除码）。
type CodeError struct {
	Err   error
	Index int
}

func (e *CodeError) Error() string {
	return fmt.Sprintf("%v (code index %d)", e.Err, e.Index)
}

func (e *CodeError) Unwrap() error { return e.Err }
