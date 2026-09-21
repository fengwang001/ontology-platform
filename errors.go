package ontology

import (
	"errors"
	"fmt"
)

// 构造参数的哨兵错误，可用 errors.Is 判定。
var (
	// ErrInvalidN 表示目标元素数 n 非法（n <= 0）。
	ErrInvalidN = errors.New("bloom: n must be positive")
	// ErrInvalidP 表示目标假阳性率 p 非法（p <= 0 或 p >= 1）。
	ErrInvalidP = errors.New("bloom: p must be in (0, 1)")
	// ErrParamMismatch 表示 Merge 两侧过滤器的 (m, k) 参数不一致。
	ErrParamMismatch = errors.New("bloom: parameter mismatch")
)

// MismatchError 描述一次参数不一致的 Merge，携带两侧的 (m, k)。
// 可用 errors.Is(err, ErrParamMismatch) 判定，也可用 errors.As 取出细节。
type MismatchError struct {
	LeftM, LeftK   uint
	RightM, RightK uint
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("%v: left (m=%d, k=%d), right (m=%d, k=%d)",
		ErrParamMismatch, e.LeftM, e.LeftK, e.RightM, e.RightK)
}

// Is 使 errors.Is(err, ErrParamMismatch) 成立。
func (e *MismatchError) Is(target error) bool { return target == ErrParamMismatch }
