// Package api 是变更流 txid 幂等去重的对外入口。
package api

import (
	"errors"
	"fmt"
	"ontology/apply"
	"reflect"
)

// 对外可判定的哨兵错误（再导出 apply 的定义，互不相同）。
var (
	ErrInvalidTxID    = apply.ErrInvalidTxID
	ErrEmptyKey       = apply.ErrEmptyKey
	ErrZeroDelta      = apply.ErrZeroDelta
	ErrInvalidRestore = apply.ErrInvalidRestore
)

// Service 对 apply.Engine 的薄封装，供外部直接调用与自检。
type Service struct {
	eng *apply.Engine
}

// New 返回空的去重服务。
func New() *Service {
	return &Service{eng: apply.New()}
}

// Apply 投递一条事件；重复 txid 幂等成功，非法输入返回哨兵错误。
func (s *Service) Apply(txid int64, key string, delta int) error {
	_, err := s.eng.Apply(txid, key, delta)
	return err
}

// Snapshot 返回状态副本与已应用 txid 升序列表。
func (s *Service) Snapshot() (map[string]int, []int64) {
	return s.eng.Snapshot()
}

// Restore 从持久化去重日志重建已应用集。
func (s *Service) Restore(applied []int64) error {
	return s.eng.Restore(applied)
}

// SelfCheck 在独立引擎上跑内置序列，核验四条不变量，全部通过返回 nil。
// 不触碰 s 自身状态，可并发调用。
func (s *Service) SelfCheck() error {
	e := apply.New()

	// 不变量 1+3：重复到达（含内容不同）幂等成功且不改任何状态。
	must := func(txid int64, key string, delta int) error {
		_, err := e.Apply(txid, key, delta)
		return err
	}
	if err := must(1, "k", 5); err != nil {
		return err
	}
	before, beforeSet := e.Snapshot()
	for i := 0; i < 2; i++ {
		if _, err := e.Apply(1, "other", 99); err != nil {
			return fmt.Errorf("selfcheck: duplicate rejected: %w", err)
		}
	}
	after, afterSet := e.Snapshot()
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeSet, afterSet) {
		return errors.New("selfcheck: duplicate apply mutated state")
	}

	// 不变量 2：与朴素参照（按到达顺序、首见 txid 才应用）逐 Key 一致。
	// 在独立引擎上从零开始，保证参照同起点。
	e = apply.New()
	type op struct {
		txid  int64
		key   string
		delta int
	}
	ops := []op{{2, "a", 3}, {3, "b", -1}, {2, "a", 3}, {4, "a", 7}, {3, "c", 2}, {5, "b", 4}}
	naive := map[string]int{}
	seen := map[int64]bool{}
	for _, o := range ops {
		if err := must(o.txid, o.key, o.delta); err != nil {
			return err
		}
		if !seen[o.txid] {
			seen[o.txid] = true
			naive[o.key] += o.delta
		}
	}
	st, set := e.Snapshot()
	if !reflect.DeepEqual(st, naive) {
		return fmt.Errorf("selfcheck: state %v != naive %v", st, naive)
	}
	for t := range seen {
		found := false
		for _, got := range set {
			if got == t {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("selfcheck: applied set missing txid %d", t)
		}
	}

	// 不变量 4：三类非法输入与非法 Restore 整体失败、不留痕。
	before, beforeSet = e.Snapshot()
	rejects := []error{
		func() error { _, err := e.Apply(0, "k", 1); return err }(),
		func() error { _, err := e.Apply(-7, "k", 1); return err }(),
		func() error { _, err := e.Apply(9, "", 1); return err }(),
		func() error { _, err := e.Apply(9, "k", 0); return err }(),
		e.Restore([]int64{1, 0, 2}),
	}
	for _, err := range rejects {
		if err == nil {
			return errors.New("selfcheck: illegal input accepted")
		}
	}
	after, afterSet = e.Snapshot()
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeSet, afterSet) {
		return errors.New("selfcheck: rejected op left a trace")
	}
	return nil
}
