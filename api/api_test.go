package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/obx"
)

// 不变量 1/3：随机交错后 Applied 等于全部已提交消息按 (csn,id) 排序；
// 模型 id 唯一，逐项相等即无重复。表驱动：多档种子。
func TestNaiveReference(t *testing.T) {
	if err := api.New(8).SelfCheck(); err != nil {
		t.Fatal(err)
	}
	for _, seed := range []int64{1, 7, 42, 99, 2026} {
		rng := rand.New(rand.NewSource(seed))
		a, want := api.New(128), []int{}
		open, id := map[string][]int{}, 0
		for i := 0; i < 80; i++ {
			n := string(rune('a' + rng.Intn(5)))
			switch rng.Intn(4) {
			case 0, 1: // 写：服务拒绝（事务已关闭）则模型同样跳过
				if err := a.Write(n, "p"); err == nil {
					id++
					open[n] = append(open[n], id)
				}
			case 2: // 提交或中止一个开启中的事务
				if len(open[n]) > 0 {
					if rng.Intn(2) == 0 {
						if err := a.Commit(n); err == nil {
							want = append(want, open[n]...)
						}
					} else {
						a.Abort(n)
					}
					open[n] = nil
				}
			case 3:
				a.Relay(rng.Intn(3) == 0)
			}
		}
		a.Relay(false)
		if got := a.Applied(); !slices.Equal(got, want) {
			t.Fatalf("seed %d: got %v want %v", seed, got, want)
		}
	}
}

// 不变量 2：被标记的消息必已投递过；崩溃只让未标记者被重投一次。
func TestMarkImpliesDelivered(t *testing.T) {
	for _, n := range []int{1, 2, 5, 9} {
		a := api.New(16)
		for i := 0; i < n; i++ {
			a.Write("t", "p")
		}
		a.Commit("t")
		d1 := a.Relay(true) // 最后一条已投递、未标记
		d2 := a.Relay(false)
		want := make([]int, n)
		for i := range want {
			want[i] = i + 1
		}
		if !slices.Equal(d1, want) || !slices.Equal(d2, want[n-1:]) {
			t.Fatalf("n=%d: d1=%v d2=%v", n, d1, d2)
		}
		if !slices.Equal(a.Applied(), want) || a.Dups() != 1 {
			t.Fatalf("n=%d: applied=%v dups=%d", n, a.Applied(), a.Dups())
		}
	}
}

// 不变量 4：三类可判定错误互不相同；被拒后状态不变、可继续正常使用。
func TestRejectKeepsState(t *testing.T) {
	a := api.New(2)
	a.Write("t", "p")
	a.Write("t", "p")
	a.Commit("t")
	a.Write("u", "p") // u 开启 1 条；此刻提交将超限
	ops := []func() error{
		func() error { return a.Write("t", "p") },
		func() error { return a.Commit("ghost") },
		func() error { return a.Abort("ghost") },
		func() error { return a.Write("u", "") },
		func() error { return a.Commit("u") },
	}
	wants := []error{obx.ErrTxUnavailable, obx.ErrTxUnavailable, obx.ErrTxUnavailable, obx.ErrEmptyPayload, obx.ErrBacklog}
	for i, op := range ops {
		if err := op(); !errors.Is(err, wants[i]) {
			t.Fatalf("case %d: got %v want %v", i, err, wants[i])
		}
	}
	a.Write("u", "p") // 被拒后 u 仍开启，可继续写入
	a.Relay(false)    // 腾出积压空间
	if err := a.Commit("u"); err != nil {
		t.Fatal(err)
	}
	a.Relay(false)
	if !slices.Equal(a.Applied(), []int{1, 2, 3, 4}) || a.Dups() != 0 {
		t.Fatalf("state changed by rejections: %v", a.Applied())
	}
}

// 并发：8 个 goroutine 各写并提交 4 个事务（每个 3 条），另一个反复 Relay（含崩溃）。
func TestConcurrent(t *testing.T) {
	a := api.New(1 << 20)
	var stop atomic.Bool
	var rwg, wg sync.WaitGroup
	rwg.Add(1)
	go func() {
		defer rwg.Done()
		for !stop.Load() {
			a.Relay(true)
		}
	}()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for tx := 0; tx < 4; tx++ {
				n := fmt.Sprintf("g%dt%d", g, tx)
				a.Write(n, "p")
				a.Write(n, "p")
				a.Write(n, "p")
				a.Commit(n)
			}
		}(g)
	}
	wg.Wait()
	stop.Store(true)
	rwg.Wait()
	a.Relay(false)
	ap := a.Applied()
	if len(ap) != 96 {
		t.Fatalf("applied %d, want 96", len(ap))
	}
	seen := map[int]bool{}
	for i, id := range ap {
		if id < 1 || id > 96 || seen[id] || (i%3 == 2 && !(ap[i-2] < ap[i-1] && ap[i-1] < id)) {
			t.Fatalf("bad applied at %d: %v", i, ap)
		}
		seen[id] = true
	}
}
