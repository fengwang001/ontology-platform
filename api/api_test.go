package api

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

// 朴素参照：反复删除相邻反号等量对直到不动点。
func naive(ops []Op) []int64 {
	s := append([]int64(nil), ops...)
	for i := 0; i+1 < len(s); i++ {
		if s[i]+s[i+1] == 0 {
			s = append(s[:i], s[i+2:]...)
			i = -1 // 从头重新扫描
		}
	}
	return s
}

// 表驱动用例 + 循环生成的随机到达顺序。
func cases() [][]Op {
	cs := [][]Op{
		{5, -5, -8, 8, 7, 2, -7, -2}, // 第三节八步
		{7, -7, 2, -2},               // 同多重集不同顺序
		{1, 1, 1, -1, -1, -1},
		{4, -4, -4, 4},
		{9},
		{},
	}
	r := rand.New(rand.NewSource(405))
	for i := 0; i < 20; i++ { // 随机到达顺序
		var s []Op
		for j := 0; j < 1+r.Intn(30); j++ {
			s = append(s, int64(1+r.Intn(9))*(1-2*int64(r.Intn(2))))
		}
		cs = append(cs, s)
	}
	return cs
}

func run(t *testing.T, depth int, ops []Op) *Normalizer {
	t.Helper()
	a := New(depth)
	for _, op := range ops {
		if err := a.Apply(op); err != nil {
			t.Fatalf("ops=%v 意外失败: %v", ops, err)
		}
	}
	return a
}

func TestChangelogMatchesNaive(t *testing.T) { // 不变量 1
	for _, ops := range cases() {
		a, sum := run(t, len(ops)+1, ops), int64(0)
		for _, op := range ops {
			sum += op
		}
		if !slices.Equal(a.Changelog(), naive(ops)) || a.Net() != sum {
			t.Fatalf("ops=%v 得到 %v net=%d，朴素参照 %v", ops, a.Changelog(), a.Net(), naive(ops))
		}
	}
}

func TestNoAdjacentCancellablePairs(t *testing.T) { // 不变量 2
	for _, ops := range cases() {
		log := run(t, len(ops)+1, ops).Changelog()
		for i := 1; i < len(log); i++ {
			if log[i-1]+log[i] == 0 {
				t.Fatalf("ops=%v changelog=%v 存在可再折叠对", ops, log)
			}
		}
	}
}

func TestReplayEqualsNet(t *testing.T) { // 不变量 3
	for _, ops := range cases() {
		a := run(t, len(ops)+1, ops)
		var replay int64
		for _, v := range a.Changelog() {
			replay += v
		}
		if replay != a.Net() {
			t.Fatalf("ops=%v 重放=%d net=%d", ops, replay, a.Net())
		}
	}
}

func TestRejectedOpsLeaveStateUnchanged(t *testing.T) { // 不变量 4
	cases := []struct {
		name  string
		setup []Op
		depth int
		bad   Op
		want  error
	}{
		{"k为零", []Op{3}, 4, 0, ErrInvalidDelta},
		{"量值不可表示", []Op{3}, 4, math.MinInt64, ErrInvalidDelta},
		{"深度超限", []Op{3, 4}, 2, 5, ErrDepthExceeded},
		{"净值溢出", []Op{math.MaxInt64}, 8, 1, ErrNetOverflow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := run(t, c.depth, c.setup)
			before, netBefore := a.Changelog(), a.Net()
			if err := a.Apply(c.bad); !errors.Is(err, c.want) {
				t.Fatalf("错误=%v，期望 %v", err, c.want)
			}
			if a.Net() != netBefore || !slices.Equal(a.Changelog(), before) {
				t.Fatalf("被拒后状态改变")
			}
			// 被拒后仍可正常使用：抵消末尾那条必然成功（不受深度限制）。
			if err := a.Apply(-c.setup[len(c.setup)-1]); err != nil {
				t.Fatalf("被拒后实例不可用: %v", err)
			}
		})
	}
}

func TestErrorsDistinct(t *testing.T) {
	if ErrInvalidDelta == ErrDepthExceeded || ErrDepthExceeded == ErrNetOverflow || ErrInvalidDelta == ErrNetOverflow {
		t.Fatal("三类哨兵错误必须互不相同")
	}
}

func TestConcurrentReads(t *testing.T) {
	a := run(t, 64, []Op{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	wantNet, wantLog := a.Net(), a.Changelog()
	var wg sync.WaitGroup
	var bad atomic.Bool
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100 && !bad.Load(); i++ {
				if a.Net() != wantNet || !slices.Equal(a.Changelog(), wantLog) || a.SelfCheck() != nil {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("并发只读结果不一致")
	}
}
