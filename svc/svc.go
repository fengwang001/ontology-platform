// Package svc 服务器管理：Acquire/Release 的下标校验与下溢校验。依赖 lc。
package svc

import (
	"errors"

	"ontology/lc"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrIndex     = errors.New("svc: server index out of range")
	ErrUnderflow = errors.New("svc: connection count underflow")
)

// Servers 包装 lc.Core，所有修改前先校验，失败不留痕。
type Servers struct {
	core *lc.Core
	n    int
}

// New 构造 n 台服务器的管理器，调用方保证 n >= 1。
func New(n int) *Servers { return &Servers{core: lc.New(n), n: n} }

func (s *Servers) valid(i int) bool { return 0 <= i && i < s.n }

// Acquire 连接建立：下标合法则计数 +1，否则返回 ErrIndex 且不改任何状态。
func (s *Servers) Acquire(i int) error {
	if !s.valid(i) {
		return ErrIndex
	}
	s.core.Incr(i)
	return nil
}

// Release 连接结束：下标合法且计数 >= 1 则计数 -1，
// 否则返回 ErrIndex 或 ErrUnderflow 且不改任何状态（不得下溢为负）。
func (s *Servers) Release(i int) error {
	if !s.valid(i) {
		return ErrIndex
	}
	if s.core.Count(i) < 1 {
		return ErrUnderflow
	}
	s.core.Decr(i)
	return nil
}

// Count 返回服务器 i 的当前连接数；下标越界返回 ErrIndex。
func (s *Servers) Count(i int) (int, error) {
	if !s.valid(i) {
		return 0, ErrIndex
	}
	return s.core.Count(i), nil
}

// Pick 返回活动连接数最少、并列下标最小的服务器下标。
func (s *Servers) Pick() int { return s.core.Pick() }
