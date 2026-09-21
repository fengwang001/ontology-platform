package ontology

import (
	"errors"
	"fmt"
)

// ErrInvalidN 表示构造时目标元素数 n 非法（n <= 0）。
var ErrInvalidN = errors.New("bloom: n must be positive")

// ErrInvalidP 表示构造时目标假阳性率 p 非法（p <= 0 或 p >= 1）。
var ErrInvalidP = errors.New("bloom: p must be in (0, 1)")

// ErrParamMismatch 表示 Merge 两侧过滤器的参数 (m, k) 不一致。
// 可用 errors.Is 判定，或用 errors.As 取出 *ParamMismatchError
// 查看双方具体的 (m, k)。
var ErrParamMismatch = errors.New("bloom: parameter mismatch")

// ParamMismatchError 描述 Merge 时两侧过滤器参数不一致的错误，
// 携带双方各自的 (m, k)。
type ParamMismatchError struct {
	AM uint64 // 左侧过滤器的位数组长度 m
	AK uint64 // 左侧过滤器的哈希个数 k
	BM uint64 // 右侧过滤器的位数组长度 m
	BK uint64 // 右侧过滤器的哈希个数 k
}

// Error 返回包含双方 (m, k) 的错误描述。
func (e *ParamMismatchError) Error() string {
	return fmt.Sprintf("%v: (m,k) differ: left=(%d,%d) right=(%d,%d)",
		ErrParamMismatch, e.AM, e.AK, e.BM, e.BK)
}

// Is 使 errors.Is(err, ErrParamMismatch) 成立。
func (e *ParamMismatchError) Is(target error) bool {
	return target == ErrParamMismatch
}
