// Package api 是对外门面：组合 ver（存储与版本戳）与 vcache（视图缓存），
// 提供 New/Write/ReadG/ReadTotal/View/SelfCheck。依赖方向 ver<-vcache<-api。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/vcache"
	"ontology/ver"
)

// 对外可判定的哨兵错误（与 ver 中的是同一批，可用 errors.Is 判定）。
var (
	ErrEmptyGroup    = ver.ErrEmptyGroup
	ErrEmptyKey      = ver.ErrEmptyKey
	ErrGroupConflict = ver.ErrGroupConflict
)

// Engine 并发安全：写独占、读共享。
type Engine struct {
	mu sync.RWMutex
	st *ver.Store
	c  *vcache.Cache
}

func New() *Engine {
	st := ver.New()
	return &Engine{st: st, c: vcache.New(st)}
}

// Write 使 (group,key) 的 Version+1、Value=val；不直接改任何缓存。
// 被拒操作（空 group / 空 key / 组冲突）不改变任何状态。
func (e *Engine) Write(group, key string, val int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st.Write(group, key, val)
}

// ReadG 返回 group 的 Value 之和（缓存过期则惰性重算）。
func (e *Engine) ReadG(group string) (int64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.c.ReadG(group)
}

// ReadTotal 返回全部记录的 Value 之和。
func (e *Engine) ReadTotal() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.c.ReadTotal()
}

// View 返回 {各组之和, 总和}，内部经 ReadG/ReadTotal 保证一致。
func (e *Engine) View() (map[string]int64, int64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.c.View()
}

// SelfCheck 在独立的内部实例上核验四条不变量，不影响本实例状态。
func (e *Engine) SelfCheck() error {
	st := ver.New()
	c := vcache.New(st)

	// 不变量 1+2：重放第三节八步序列，读结果必须等于批量重算。
	steps := []struct {
		g, k string
		v    int64
	}{{"g0", "a", 5}, {"g0", "b", 3}, {"g1", "c", 7}, {"g0", "b", 10}}
	wantG := []int64{5, 8}
	wantT := []int64{15, 22}
	for i, w := range steps {
		if err := st.Write(w.g, w.k, w.v); err != nil {
			return fmt.Errorf("selfcheck write: %w", err)
		}
		if i < 2 {
			got, _ := c.ReadG("g0")
			if got != wantG[i] || got != st.SumGroup("g0") {
				return fmt.Errorf("selfcheck ReadG step %d: got %d", i, got)
			}
		} else {
			if got := c.ReadTotal(); got != wantT[i-2] || got != st.SumTotal() {
				return fmt.Errorf("selfcheck ReadTotal step %d: got %d", i, got)
			}
		}
	}

	// 不变量 3：版本戳单调不回退（确定性伪随机写序列）。
	seed := int64(1)
	rnd := func(n int64) int64 { seed = (seed*6364136223846793005 + 1442695040888963407) >> 33; return seed % n }
	prev := st.TotalStamp()
	for i := 0; i < 200; i++ {
		g := fmt.Sprintf("g%d", rnd(4))
		k := fmt.Sprintf("k%d", rnd(40))
		if err := st.Write(g, k, rnd(1000)); err != nil && !errors.Is(err, ver.ErrGroupConflict) {
			return fmt.Errorf("selfcheck mono write: %w", err)
		}
		if cur := st.TotalStamp(); cur < prev {
			return fmt.Errorf("selfcheck: totalStamp regressed %d -> %d", prev, cur)
		}
		prev = st.TotalStamp()
	}

	// 不变量 4：三类拒绝互不相同且不留痕（全新 Store 上确定性检查）。
	rs := ver.New()
	if err := rs.Write("g0", "k0", 1); err != nil {
		return fmt.Errorf("selfcheck setup: %w", err)
	}
	before := rs.Snapshot()
	stampBefore := rs.TotalStamp()
	rejects := []error{
		rs.Write("", "x", 1),
		rs.Write("g0", "", 1),
		rs.Write("other", "k0", 1), // k0 已绑定 g0
	}
	if !errors.Is(rejects[0], ver.ErrEmptyGroup) ||
		!errors.Is(rejects[1], ver.ErrEmptyKey) ||
		!errors.Is(rejects[2], ver.ErrGroupConflict) {
		return errors.New("selfcheck: reject errors not distinguishable")
	}
	if rs.TotalStamp() != stampBefore {
		return errors.New("selfcheck: rejected write changed totalStamp")
	}
	for g, m := range rs.Snapshot() {
		for k, v := range m {
			if before[g][k] != v {
				return errors.New("selfcheck: rejected write changed a value")
			}
		}
	}
	return nil
}
