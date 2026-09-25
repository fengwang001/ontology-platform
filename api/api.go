// Package api 是对外门面：New/Assign/Put/Get/BeginDrain/Migrate/Count/SelfCheck。
// 依赖 router；默认实例带 P0、P1、P2 三个 Active 分区。
package api

import (
	"errors"
	"fmt"

	"ontology/router"
)

// API 是分区亲和路由系统的对外句柄。
type API struct {
	r *router.Router
}

// New 创建带 P0、P1、P2 三个 Active 分区的实例。
func New() *API { return &API{r: router.New(0, 1, 2)} }

// Assign 声明 key 的亲和分区。
func (a *API) Assign(key string, p int) error { return a.r.Assign(key, p) }

// Put 写 key 的值。
func (a *API) Put(key, val string) error { return a.r.Put(key, val) }

// Get 读 key 的值；无值返回 ("", false)。
func (a *API) Get(key string) (string, bool) { return a.r.Get(key) }

// BeginDrain 把分区置为 Draining。
func (a *API) BeginDrain(p int) error { return a.r.BeginDrain(p) }

// Migrate 把 from 的全部 Key 原子迁到 to。
func (a *API) Migrate(from, to int) error { return a.r.Migrate(from, to) }

// Count 返回 p 当前拥有的 Key 数。
func (a *API) Count(p int) int { return a.r.Count(p) }

// SelfCheck 在独立的内部实例上跑内置操作序列，核验四条不变量。
// 不触碰接收者状态，可并发调用。
func (a *API) SelfCheck() error {
	r := router.New(0, 1, 2)
	accepted := map[string]int{} // 只统计被接受的 Assign：key→owner
	recompute := func() map[int]int {
		out := map[int]int{0: 0, 1: 0, 2: 0}
		for _, p := range accepted {
			out[p]++
		}
		return out
	}
	checkCounts := func(tag string) error { // 不变量 1：与批量重算一致
		want := recompute()
		sum := 0
		for p := 0; p <= 2; p++ {
			if got := r.Count(p); got != want[p] {
				return fmt.Errorf("%s: Count(%d)=%d want %d", tag, p, got, want[p])
			}
			sum += want[p]
		}
		if sum != len(accepted) {
			return fmt.Errorf("%s: sum=%d want %d", tag, sum, len(accepted))
		}
		return nil
	}
	steps := []struct {
		run  func() error
		want error // nil 表示应成功
	}{
		{func() error { return r.Assign("a", 0) }, nil},
		{func() error { return r.Assign("b", 0) }, nil},
		{func() error { return r.Assign("c", 1) }, nil},
		{func() error { return r.Assign("d", 2) }, nil},
		{func() error { return r.BeginDrain(2) }, nil},
		{func() error { return r.Assign("e", 2) }, router.ErrAffinityConflict},
		{func() error { return r.Put("d", "x") }, router.ErrAffinityConflict},
	}
	for i, s := range steps { // 不变量 3、4：排空冲突被拒且不留痕
		before := recompute()
		err := s.run()
		if !errors.Is(err, s.want) {
			return fmt.Errorf("step %d: got %v want %v", i, err, s.want)
		}
		if err == nil {
			switch i {
			case 0:
				accepted["a"] = 0
			case 1:
				accepted["b"] = 0
			case 2:
				accepted["c"] = 1
			case 3:
				accepted["d"] = 2
			}
		} else {
			for p := 0; p <= 2; p++ { // 被拒后状态不变
				if r.Count(p) != before[p] {
					return fmt.Errorf("step %d: rejected op changed Count(%d)", i, p)
				}
			}
		}
		if err := checkCounts(fmt.Sprintf("step %d", i)); err != nil {
			return err
		}
	}
	if v, ok := r.Get("d"); ok || v != "" { // Draining 仍可读，但无值
		return fmt.Errorf("Get(d)=%q,%v before migrate", v, ok)
	}
	if err := r.Migrate(2, 0); err != nil { // 不变量 2：迁移一致性
		return fmt.Errorf("migrate: %v", err)
	}
	for k := range accepted {
		if accepted[k] == 2 {
			accepted[k] = 0
		}
	}
	if err := checkCounts("post-migrate"); err != nil {
		return err
	}
	if r.Count(2) != 0 {
		return errors.New("from partition not empty after migrate")
	}
	if v, ok := r.Get("d"); ok || v != "" { // 被拒的 Put 不留痕：仍无值
		return fmt.Errorf("Get(d)=%q,%v after migrate, want empty", v, ok)
	}
	if err := r.Put("d", "x"); err != nil { // 迁移后 d 归属 Active 的 P0，可写
		return fmt.Errorf("Put(d) after migrate: %v", err)
	}
	if v, ok := r.Get("d"); !ok || v != "x" {
		return fmt.Errorf("Get(d)=%q,%v after put", v, ok)
	}
	return checkCounts("final")
}
