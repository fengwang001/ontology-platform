// Package fkjoin 实现两表外键内连接组件：左表行携带可空外键，
// 通过订阅右表并经由 FIFO 响应队列异步维护内连接结果。
package fkjoin

import (
	"errors"
	"fmt"
)

// 可区分的拒绝原因。所有被拒绝的操作都不会改变
// 两表、订阅、响应队列、结果集与丢弃计数。
var (
	// ErrEmptyKey 表示外键或右表键为空字符串。
	ErrEmptyKey = errors.New("fkjoin: empty key")
	// ErrEmptyQueue 表示对空响应队列执行投递。
	ErrEmptyQueue = errors.New("fkjoin: response queue is empty")
	// ErrPendingLimit 表示待投递响应数量达到上限。
	ErrPendingLimit = errors.New("fkjoin: pending response limit reached")
)

// RejectError 描述一次被拒绝的操作及其原因。
type RejectError struct {
	Op     string // 被拒绝的操作名
	Reason error  // 拒绝原因，可用 errors.Is 判定
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("fkjoin: %s rejected: %v", e.Op, e.Reason)
}

// Unwrap 返回拒绝原因，便于 errors.Is(err, ErrEmptyKey) 等判定。
func (e *RejectError) Unwrap() error { return e.Reason }

func reject(op string, reason error) error {
	return &RejectError{Op: op, Reason: reason}
}
