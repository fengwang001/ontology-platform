// Package api 是 G 计数器注册表的对外门面，并提供不变量自检。
package api

import (
	"errors"
	"fmt"

	"ontology/gc"
	"ontology/reg"
)

// API 包装注册表，对外提供全部操作。
type API struct{ r *reg.Registry }

// New 返回空 API。
func New() *API { return &API{r: reg.New()} }

// Set 以 name 注册计数器。
func (a *API) Set(name string, c map[int]int) error { return a.r.Set(name, gc.Counter(c)) }

// Inc 把 name 中 node 的条目加 k。
func (a *API) Inc(name string, node, k int) error { return a.r.Inc(name, node, k) }

// MergeInto 把 dst 更新为 dst 与 src 的逐条目 max，返回 dst 新值。
func (a *API) MergeInto(dst, src string) (int, error) { return a.r.MergeInto(dst, src) }

// Value 返回 name 的所有条目之和。
func (a *API) Value(name string) (int, error) { return a.r.Value(name) }

// Snapshot 返回 name 计数器的副本。
func (a *API) Snapshot(name string) (map[int]int, error) {
	c, err := a.r.Snapshot(name)
	return map[int]int(c), err
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (a *API) SelfCheck() error {
	// 不变量 1+2：Merge 与朴素重算一致、交换律、幂等
	x := gc.Counter{0: 4, 2: 1}
	y := gc.Counter{1: 6, 2: 3}
	xy, err := gc.Merge(x, y)
	if err != nil {
		return err
	}
	yx, _ := gc.Merge(y, x)
	xx, _ := gc.Merge(x, x)
	if gc.Value(xy) != 13 || !same(xy, yx) || !same(xx, x) {
		return errors.New("api: 不变量1/2 自检失败")
	}
	// 不变量 3：值单调——内置序列逐步核验 Value 不减
	r := reg.New()
	seq := []struct {
		run  func() error
		name string
	}{
		{func() error { return r.Set("s", nil) }, "s"},
		{func() error { return r.Inc("s", 0, 5) }, "s"},
		{func() error { return r.Set("t", nil) }, "s"},
		{func() error { return r.Inc("t", 1, 3) }, "s"},
		{func() error { _, e := r.MergeInto("t", "s"); return e }, "t"},
	}
	prev := 0
	for _, op := range seq {
		if err := op.run(); err != nil {
			return err
		}
		v, err := r.Value(op.name)
		if err != nil {
			return err
		}
		if v < prev {
			return fmt.Errorf("api: 不变量3 自检失败 Value 减小 %d<%d", v, prev)
		}
		prev = v
	}
	// 不变量 4：失败不留痕——被拒操作前后状态一致
	before, _ := r.Value("s")
	if r.Inc("s", 0, 0) == nil || r.Inc("s", -1, 1) == nil {
		return errors.New("api: 不变量4 自检失败 拒绝操作未报错")
	}
	if err := r.Set("bad", gc.Counter{0: -1}); err != gc.ErrNegativeEntry {
		return errors.New("api: 不变量4 自检失败 负条目未拒绝")
	}
	after, _ := r.Value("s")
	if before != after {
		return fmt.Errorf("api: 不变量4 自检失败 状态被改 %d!=%d", before, after)
	}
	return nil
}

func same(x, y gc.Counter) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if y[k] != v {
			return false
		}
	}
	return true
}
