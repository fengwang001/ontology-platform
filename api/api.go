// Package api 是对外接口：包装 est.Estimator，并提供内置自检 SelfCheck。
package api

import (
	"errors"
	"fmt"

	"ontology/est"
	"ontology/hll"
)

// ErrSelfCheck：SelfCheck 发现不变量被破坏。
var ErrSelfCheck = errors.New("api: self-check failed")

// Estimator 是对外基数估计器。
type Estimator struct{ e *est.Estimator }

// New 创建估计器；p < 1 或 S < 0 报可判定错误。
func New(p, S int) (*Estimator, error) {
	e, err := est.New(p, S)
	if err != nil {
		return nil, err
	}
	return &Estimator{e: e}, nil
}

// Add 加入一次 key；空串报可判定错误。
func (a *Estimator) Add(key string) error { return a.e.Add(key) }

// Remove 撤回一次 key；空串或键不存在报可判定错误。
func (a *Estimator) Remove(key string) error { return a.e.Remove(key) }

// Card 返回当前基数估计。
func (a *Estimator) Card() float64 { return a.e.Card() }

// batchRanks 把精确不同键集合重新批量计算一遍秩（不变量 1 的参照）。
func batchRanks(p int, live map[string]int) []int {
	sk, err := hll.New(p)
	if err != nil {
		panic(err)
	}
	for k := range live {
		sk.Add(k)
	}
	return sk.Ranks()
}

func equalRanks(x, y []int) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// SelfCheck 对内置 Add/Remove 序列核验四条不变量，全部通过返回 nil。
func (a *Estimator) SelfCheck() error {
	// 不变量 1：任意序列后与批量重算逐寄存器一致 / 稀疏精确。
	e1, err := est.New(4, 8)
	if err != nil {
		return err
	}
	live := map[string]int{}
	for i := 0; i < 200; i++ {
		if i%3 == 2 && len(live) > 0 {
			var k string
			for k = range live {
				break
			}
			if err := e1.Remove(k); err != nil {
				return fmt.Errorf("%w: inv1 remove: %v", ErrSelfCheck, err)
			}
			if live[k]--; live[k] == 0 {
				delete(live, k)
			}
		} else {
			k := fmt.Sprintf("k%d", (i*37)%53)
			if err := e1.Add(k); err != nil {
				return fmt.Errorf("%w: inv1 add: %v", ErrSelfCheck, err)
			}
			live[k]++
		}
		if e1.Dense() {
			if !equalRanks(e1.Ranks(), batchRanks(4, live)) {
				return fmt.Errorf("%w: inv1 ranks diverge at op %d", ErrSelfCheck, i)
			}
		} else if e1.Card() != float64(len(live)) {
			return fmt.Errorf("%w: inv1 sparse card at op %d", ErrSelfCheck, i)
		}
	}
	// 不变量 2：Add(k) 紧跟 Remove(k) 精确还原；删最大秩回落次大秩。
	e2, _ := est.New(2, 0)
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		_ = e2.Add(k)
	}
	snap, snapCard := e2.Ranks(), e2.Card()
	_ = e2.Add("probe")
	_ = e2.Remove("probe")
	if !equalRanks(e2.Ranks(), snap) || e2.Card() != snapCard {
		return fmt.Errorf("%w: inv2 add/remove not reversible", ErrSelfCheck)
	}
	_ = e2.Remove("e") // e 是寄存器 1 最大秩 2，a 秩 1 仍在
	if r := e2.Ranks(); r[1] != 1 {
		return fmt.Errorf("%w: inv2 rank fallback r1=%d want 1", ErrSelfCheck, r[1])
	}
	// 不变量 3：恰在 d==S+1 转稠密一次，之后不回落。
	e3, _ := est.New(2, 3)
	for _, k := range []string{"a", "b", "c"} {
		_ = e3.Add(k)
	}
	if e3.Dense() {
		return fmt.Errorf("%w: inv3 dense before S+1", ErrSelfCheck)
	}
	_ = e3.Add("d")
	if !e3.Dense() {
		return fmt.Errorf("%w: inv3 not dense at S+1", ErrSelfCheck)
	}
	for _, k := range []string{"a", "b", "c", "d"} {
		_ = e3.Remove(k)
	}
	if !e3.Dense() {
		return fmt.Errorf("%w: inv3 fell back to sparse", ErrSelfCheck)
	}
	// 不变量 4：被拒操作不改变任何状态，之后仍可正常使用。
	e4, _ := est.New(2, 1)
	_ = e4.Add("a")
	_ = e4.Add("b")
	cBefore, rBefore := e4.Card(), e4.Ranks()
	if e4.Add("") == nil || e4.Remove("") == nil || e4.Remove("ghost") == nil {
		return fmt.Errorf("%w: inv4 invalid op accepted", ErrSelfCheck)
	}
	if _, err := est.New(0, 1); err == nil {
		return fmt.Errorf("%w: inv4 bad p accepted", ErrSelfCheck)
	}
	if _, err := est.New(1, -1); err == nil {
		return fmt.Errorf("%w: inv4 bad S accepted", ErrSelfCheck)
	}
	if e4.Card() != cBefore || !equalRanks(e4.Ranks(), rBefore) || e4.Add("c") != nil {
		return fmt.Errorf("%w: inv4 state changed by rejected op", ErrSelfCheck)
	}
	return nil
}
