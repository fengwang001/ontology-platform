package gateway

import (
	"errors"
	"fmt"
)

// ConflictError 表示同一个幂等键被配上了不同的请求体。
// 调用方可用 errors.As 判定冲突。
type ConflictError struct {
	Key string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("idempotency key %q conflict: request body differs from the original", e.Key)
}

// IsConflict 便捷判定任意错误是否为请求体冲突。
func IsConflict(err error) bool {
	var conflict *ConflictError
	return errors.As(err, &conflict)
}
