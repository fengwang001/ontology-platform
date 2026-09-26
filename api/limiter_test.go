package api

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

var eightSteps = []struct {
	t, need int64
	allow   bool
	tokens  int64
}{
	{0, 15, true, 5}, {0, 8, false, 5}, {3, 10, true, 1}, {3, 5, false, 1},
	{8, 12, false, 11}, {9, 6, true, 7}, {20, 18, true, 2}, {20, 3, false, 2},
}

func TestNewInvalid(t *testing.T) { // 非法配置 + 三类哨兵互异 + SelfCheck
	for _, c := range [][2]int64{{0, 2}, {-1, 2}, {20, 0}, {20, -3}, {0, 0}} {
		if l, err := New(c[0], c[1]); !errors.Is(err, ErrInvalidConfig) || l != nil {
			t.Fatalf("New(%d,%d)=(%v,%v), want (nil,ErrInvalidConfig)", c[0], c[1], l, err)
		}
	}
	if l, err := New(1, 1); err != nil || l == nil {
		t.Fatalf("New(1,1)=(%v,%v), want non-nil,nil", l, err)
	}
	if errors.Is(ErrInvalidConfig, ErrInvalidNeed) || errors.Is(ErrInvalidConfig, ErrClockRollback) ||
		errors.Is(ErrInvalidNeed, ErrClockRollback) {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	l, _ := New(20, 2)
	if err := l.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
func TestNaiveReference(t *testing.T) { // 不变量 1：先钉死八步表，再随机序列比对
	l, _ := New(20, 2)
	ref, prev := int64(20), int64(0)
	for i, w := range eightSteps {
		ref = min(int64(20), ref+(w.t-prev)*2)
		prev = w.t
		want := ref >= w.need
		if want {
			ref -= w.need
		}
		got, err := l.Allow(w.t, w.need)
		if err != nil || got != want || l.Tokens() != ref || got != w.allow || l.Tokens() != w.tokens {
			t.Fatalf("fixed step %d: got (%v,%d), want (%v,%d)", i, got, l.Tokens(), w.allow, w.tokens)
		}
	}
	rng := rand.New(rand.NewSource(97))
	for iter := 0; iter < 500; iter++ {
		capacity, rate := int64(1+rng.Intn(40)), int64(1+rng.Intn(8))
		l, _ := New(capacity, rate)
		ref, prev := capacity, int64(0)
		for step := 0; step < 40; step++ {
			ts, need := prev+int64(rng.Intn(6)), int64(1+rng.Intn(30))
			ref = min(capacity, ref+(ts-prev)*rate)
			prev = ts
			want := ref >= need
			if want {
				ref -= need
			}
			got, err := l.Allow(ts, need)
			if err != nil || got != want || l.Tokens() != ref {
				t.Fatalf("iter %d step %d: got (%v,%d), want (%v,%d)", iter, step, got, l.Tokens(), want, ref)
			}
		}
	}
}
func TestTokensBounds(t *testing.T) { // 不变量 2：令牌始终在 [0, capacity]
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 300; iter++ {
		capacity, rate := int64(1+rng.Intn(60)), int64(1+rng.Intn(12))
		l, _ := New(capacity, rate)
		var clock int64
		for step := 0; step < 50; step++ {
			clock += int64(rng.Intn(7))
			if _, err := l.Allow(clock, int64(1+rng.Intn(50))); err != nil {
				t.Fatal(err)
			}
			if toks := l.Tokens(); toks < 0 || toks > capacity {
				t.Fatalf("tokens %d out of [0,%d]", toks, capacity)
			}
		}
	}
}
func TestDeterministic(t *testing.T) { // 不变量 3：同序列双实例逐拍一致
	rng := rand.New(rand.NewSource(31337))
	a, _ := New(20, 2)
	b, _ := New(20, 2)
	var clock int64
	for i := 0; i < 60; i++ {
		clock += int64(rng.Intn(5))
		need := int64(1 + rng.Intn(25))
		x, ex := a.Allow(clock, need)
		y, ey := b.Allow(clock, need)
		if ex != nil || ey != nil || x != y || a.Tokens() != b.Tokens() {
			t.Fatalf("step %d diverged: (%v,%d) vs (%v,%d)", i, x, a.Tokens(), y, b.Tokens())
		}
	}
}
func TestFailureLeavesNoTrace(t *testing.T) { // 不变量 4：非法调用不改状态，之后与对照桶一致
	l, _ := New(20, 2)
	l.Allow(5, 10) // tokens=10, last=5
	for _, c := range []struct {
		ts, need int64
		want     error
	}{{5, 0, ErrInvalidNeed}, {5, -4, ErrInvalidNeed}, {4, 1, ErrClockRollback}} {
		before := l.Tokens()
		got, err := l.Allow(c.ts, c.need)
		if got || !errors.Is(err, c.want) || l.Tokens() != before {
			t.Fatalf("Allow(%d,%d)=(%v,%v,tok=%d), want (false,%v,%d)", c.ts, c.need, got, err, l.Tokens(), c.want, before)
		}
	}
	ctrl, _ := New(20, 2)
	ctrl.Allow(5, 10)
	for _, r := range [][2]int64{{6, 3}, {6, 20}, {10, 15}} {
		g1, e1 := l.Allow(r[0], r[1])
		g2, e2 := ctrl.Allow(r[0], r[1])
		if g1 != g2 || !errors.Is(e1, e2) || l.Tokens() != ctrl.Tokens() {
			t.Fatalf("after failures, diverged at (%d,%d)", r[0], r[1])
		}
	}
}
func TestConcurrentSameTimestamp(t *testing.T) { // N goroutine 同时间戳，全放行且剩 capacity-N
	const N = 64
	l, _ := New(N, 1)
	var wg sync.WaitGroup
	var allowed atomic.Int64
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow(10, 1); ok {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != N || l.Tokens() != 0 {
		t.Fatalf("allowed=%d tokens=%d, want %d and 0", allowed.Load(), l.Tokens(), N)
	}
}
