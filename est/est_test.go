package est

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"ontology/hll"
)

// replay 对 e 重放确定性伪随机操作序列，并返回精确多集镜像。
func replay(t *testing.T, e *Estimator, pool, ops int) map[string]int {
	t.Helper()
	live := map[string]int{}
	for i := 0; i < ops; i++ {
		k := fmt.Sprintf("k%d", (i*31+7)%pool)
		if i%5 == 3 && live[k] > 0 {
			if err := e.Remove(k); err != nil {
				t.Fatalf("Remove(%s): %v", k, err)
			}
			if live[k]--; live[k] == 0 {
				delete(live, k)
			}
		} else if err := e.Add(k); err != nil {
			t.Fatalf("Add(%s): %v", k, err)
		} else {
			live[k]++
		}
	}
	return live
}
func TestBatchConsistency(t *testing.T) { // 不变量 1
	for _, c := range []struct{ p, s, pool, ops int }{
		{2, 3, 5, 40}, {4, 0, 50, 300}, {6, 20, 200, 2000}, {3, 100, 30, 200},
	} {
		e, err := New(c.p, c.s)
		if err != nil {
			t.Fatal(err)
		}
		live := replay(t, e, c.pool, c.ops)
		if !e.Dense() {
			if e.Card() != float64(len(live)) {
				t.Fatalf("%+v: 稀疏 Card=%v 精确=%d", c, e.Card(), len(live))
			}
			continue
		}
		fresh, _ := hll.New(c.p)
		for k := range live {
			fresh.Add(k)
		}
		if !slices.Equal(e.Ranks(), fresh.Ranks()) || e.Card() != fresh.Card() {
			t.Fatalf("%+v: Ranks/Card 与批量重算不一致", c)
		}
	}
}
func TestRevokeReversible(t *testing.T) { // 不变量 2
	for _, p := range []int{1, 2, 5} {
		e, _ := New(p, 0)
		replay(t, e, 100, 500)
		before, c0 := e.Ranks(), e.Card()
		e.Add("probe-key")
		e.Remove("probe-key")
		if !slices.Equal(e.Ranks(), before) || e.Card() != c0 {
			t.Fatalf("p=%d: Add 后 Remove 未精确还原", p)
		}
	}
	// 删除最大秩回落次大秩：p=2 时 a→(1,1)、e→(1,2) 同寄存器 1
	e, _ := New(2, 0)
	e.Add("a")
	e.Add("e")
	for i, want := range []int{2, 1, 0} { // 加完、删 e、再删 a
		if i > 0 {
			e.Remove([]string{"", "e", "a"}[i])
		}
		if r := e.Ranks(); r[1] != want {
			t.Fatalf("阶段 %d: r1=%d, 应为 %d", i, r[1], want)
		}
	}
}
func TestThresholdOnce(t *testing.T) { // 不变量 3
	for _, s := range []int{0, 1, 3, 10} {
		e, _ := New(4, s)
		for i := 0; i < s; i++ {
			e.Add(fmt.Sprintf("k%d", i))
		}
		if e.Dense() || e.Card() != float64(s) {
			t.Fatalf("S=%d: d==S 时须稀疏且精确", s)
		}
		e.Add("trigger")
		if !e.Dense() {
			t.Fatalf("S=%d: d==S+1 时须转稠密", s)
		}
		for i := 0; i < s; i++ { // 降回 S 以下也不回落
			e.Remove(fmt.Sprintf("k%d", i))
		}
		if !e.Dense() {
			t.Fatalf("S=%d: 稠密不得回落", s)
		}
	}
}
func TestFailureAtomic(t *testing.T) { // 不变量 4
	if _, err := New(0, 1); !errors.Is(err, hll.ErrBadP) {
		t.Fatal("p<1 须报 ErrBadP")
	}
	if _, err := New(1, -1); !errors.Is(err, ErrBadS) {
		t.Fatal("S<0 须报 ErrBadS")
	}
	for _, s := range []int{3, 0} { // 稀疏与稠密两种模式
		e, _ := New(2, s)
		e.Add("a") // p=2 时 a→桶(1,1)，"never-added"→桶(3,1) 必空
		c0, r0 := e.Card(), e.Ranks()
		for _, tc := range []struct {
			op   func() error
			want error
		}{
			{func() error { return e.Add("") }, hll.ErrEmptyKey},
			{func() error { return e.Remove("") }, hll.ErrEmptyKey},
			{func() error { return e.Remove("never-added") }, hll.ErrNotFound},
		} {
			if err := tc.op(); !errors.Is(err, tc.want) {
				t.Fatalf("S=%d: err=%v, 应 %v", s, err, tc.want)
			}
		}
		if e.Card() != c0 || !slices.Equal(e.Ranks(), r0) || e.Add("ok") != nil {
			t.Fatalf("S=%d: 被拒操作留下痕迹或无法继续使用", s)
		}
	}
	if len(map[error]bool{hll.ErrBadP: true, ErrBadS: true, hll.ErrEmptyKey: true, hll.ErrNotFound: true}) != 4 {
		t.Fatal("四类错误必须互不相同")
	}
}
func TestTouchedOne(t *testing.T) { // 复杂度：稠密单次增删只动 1 个寄存器
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		e, _ := New(8, 0)
		for i := 0; i < n; i++ {
			e.Add(fmt.Sprintf("k%d", i))
		}
		e.Add("one-more")
		if e.touched != 1 {
			t.Fatalf("N=%d: Add 修改寄存器数=%d, 应恒为 1", n, e.touched)
		}
		e.Remove("one-more")
		if e.touched != 1 {
			t.Fatalf("N=%d: Remove 修改寄存器数=%d, 应恒为 1", n, e.touched)
		}
	}
}
