// Package api 对外暴露 txid 幂等去重服务。
package api

import (
	"errors"
	"fmt"

	"ontology/apply"
)

// 可判定哨兵错误，三者互不相同。
var (
	ErrInvalidTxID = errors.New("txid 非法：必须为正整数")
	ErrEmptyKey    = errors.New("key 非法：空串")
	ErrZeroDelta   = errors.New("delta 非法：为 0")
)

// Service 是对外门面，校验参数后委托 apply.Applier。
type Service struct{ a *apply.Applier }

// New 返回空服务。
func New() *Service { return &Service{a: apply.New()} }

// Apply 校验参数（任一非法即整体失败、不留痕），再原子地查重并应用。
func (s *Service) Apply(txid int64, key string, delta int) error {
	if txid <= 0 {
		return ErrInvalidTxID
	}
	if key == "" {
		return ErrEmptyKey
	}
	if delta == 0 {
		return ErrZeroDelta
	}
	s.a.Apply(txid, key, delta)
	return nil
}

// Snapshot 返回状态副本与已应用 txid 升序列表。
func (s *Service) Snapshot() (map[string]int, []int64) { return s.a.Snapshot() }

// Restore 校验列表（含非法 txid 即整体失败、不重建），再重建已应用集。
func (s *Service) Restore(applied []int64) error {
	for _, txid := range applied {
		if txid <= 0 {
			return ErrInvalidTxID
		}
	}
	s.a.Restore(applied)
	return nil
}

// SelfCheck 在独立内部实例上核验四条不变量，全部通过返回 nil。
func (s *Service) SelfCheck() error {
	fresh := New()
	// 不变量 1+3：重复 Apply（含内容不同）不改变状态与已应用集。
	steps := []struct {
		txid  int64
		key   string
		delta int
	}{{1, "k", 5}, {2, "k", 3}, {1, "k", 5}, {3, "k", 5}, {2, "m", 7}, {2, "x", 9}}
	for _, st := range steps {
		if err := fresh.Apply(st.txid, st.key, st.delta); err != nil {
			return fmt.Errorf("selfcheck apply: %w", err)
		}
	}
	state, set := fresh.Snapshot()
	if state["k"] != 13 || len(state) != 1 {
		return fmt.Errorf("selfcheck 不变量1/3: state=%v", state)
	}
	if len(set) != 3 || set[0] != 1 || set[1] != 2 || set[2] != 3 {
		return fmt.Errorf("selfcheck 不变量1/3: set=%v", set)
	}
	// 不变量 2：与朴素参照一致（首现 txid 才应用）。
	naive := map[string]int{}
	seen := map[int64]bool{}
	for _, st := range steps {
		if !seen[st.txid] {
			seen[st.txid] = true
			naive[st.key] += st.delta
		}
	}
	for k, v := range naive {
		if state[k] != v {
			return fmt.Errorf("selfcheck 不变量2: state[%q]=%d want %d", k, state[k], v)
		}
	}
	// 不变量 4：被拒操作不留痕，之后仍可正常使用。
	before, beforeSet := fresh.Snapshot()
	for _, bad := range []struct {
		txid  int64
		key   string
		delta int
	}{{0, "k", 1}, {-1, "k", 1}, {9, "", 1}, {9, "k", 0}} {
		if err := fresh.Apply(bad.txid, bad.key, bad.delta); err == nil {
			return fmt.Errorf("selfcheck 不变量4: %+v 应被拒绝", bad)
		}
	}
	if err := fresh.Restore([]int64{1, -2}); !errors.Is(err, ErrInvalidTxID) {
		return fmt.Errorf("selfcheck 不变量4: Restore 非法列表应报 ErrInvalidTxID, got %v", err)
	}
	after, afterSet := fresh.Snapshot()
	if len(after) != len(before) || len(afterSet) != len(beforeSet) {
		return fmt.Errorf("selfcheck 不变量4: 被拒操作留下痕迹")
	}
	for k, v := range before {
		if after[k] != v {
			return fmt.Errorf("selfcheck 不变量4: state[%q] 被改", k)
		}
	}
	return fresh.Apply(9, "k", 1) // 拒绝后仍可用
}
