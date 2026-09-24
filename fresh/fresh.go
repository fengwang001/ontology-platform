// Package fresh 维护提交位点 H 与已应用位点 A 的单调推进及越界判定。
// 本包不加锁，并发安全由上层（lag）保证。
package fresh

import "errors"

var (
	// ErrCommitGap：Commit(n) 的 n 不恰好等于 H+1。
	ErrCommitGap = errors.New("fresh: commit 不连续")
	// ErrApplyRange：Apply(n) 不满足 A < n <= H。
	ErrApplyRange = errors.New("fresh: apply 越界")
)

// State 是位点状态：H 最新已提交，A 读端可见的最新已应用，恒有 A <= H。
type State struct {
	H int64
	A int64
}

// New 返回初始状态 H=-1, A=-1。
func New() *State { return &State{H: -1, A: -1} }

// Commit 把 H 推进到 n；n 必须恰好是 H+1，否则整体失败且状态不变。
func (s *State) Commit(n int64) error {
	if n != s.H+1 {
		return ErrCommitGap
	}
	s.H = n
	return nil
}

// Apply 把 A 推进到 n；必须满足 A < n <= H，否则整体失败且状态不变。
func (s *State) Apply(n int64) error {
	if n <= s.A || n > s.H {
		return ErrApplyRange
	}
	s.A = n
	return nil
}

// Target 计算冻结目标位点 T = H - lag（调用方须先校验 lag >= 0）。
func (s *State) Target(lag int64) int64 { return s.H - lag }
