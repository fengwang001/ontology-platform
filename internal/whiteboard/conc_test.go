package whiteboard

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentConsistency 用多 goroutine 混合写入与查询，
// 在 -race 下验证内部结构（treap 父指针、size、名次）无数据竞争且始终自洽。
func TestConcurrentConsistency(t *testing.T) {
	b := New()
	for i := 0; i < 200; i++ {
		mustAdd(t, b, "seed", fmt.Sprintf("x%03d", i), 0)
	}
	var clock int64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for k := 0; k < 300; k++ {
				now := atomic.AddInt64(&clock, 1)
				id := fmt.Sprintf("x%03d", (worker*31+k)%200)
				anchor := fmt.Sprintf("x%03d", (worker*17+k*7)%200)
				if id == anchor {
					anchor = fmt.Sprintf("x%03d", (int(num(id))+1)%200)
				}
				var side Side
				if k%2 == 0 {
					side = Above
				} else {
					side = Below
				}
				_ = b.Reorder("seed", id, anchor, side, b.Rev(), now)
				if k%5 == 0 {
					_ = b.Lock("seed", id, 60, now)
				}
				if k%7 == 0 {
					_ = b.Unlock("seed", id, now)
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 2000; k++ {
				order := b.Order()
				if len(order) != 200 {
					t.Errorf("order len changed: %d", len(order))
					return
				}
				id := order[(k)%200]
				rank, err := b.Rank(id)
				if err != nil || rank < 1 || rank > 200 {
					t.Errorf("rank %s=%d err=%v", id, rank, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	// 最终结构不变量：名次 1..n 唯一覆盖所有元素。
	seen := map[int]bool{}
	for _, id := range b.Order() {
		r, err := b.Rank(id)
		if err != nil {
			t.Fatal(err)
		}
		if seen[r] {
			t.Fatalf("duplicate rank %d", r)
		}
		seen[r] = true
	}
	if len(seen) != 200 {
		t.Fatalf("rank coverage %d", len(seen))
	}
}

func num(s string) int64 {
	var n int64
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			n = n*10 + int64(s[i]-'0')
		}
	}
	return n
}

// TestConcurrentGroupMove 多 goroutine 并发对不同组合做整体移动，
// 每次成功移动后验证组合内部相对次序未被打乱。
func TestConcurrentGroupMove(t *testing.T) {
	b := New()
	const groups = 10
	const perGroup = 4
	var ts int64 = 0
	for i := 0; i < groups*perGroup; i++ {
		ts++
		mustAdd(t, b, "seed", fmt.Sprintf("e%03d", i), ts)
	}
	for g := 0; g < groups; g++ {
		ts++
		var ids []string
		for j := 0; j < perGroup; j++ {
			ids = append(ids, fmt.Sprintf("e%03d", g*perGroup+j))
		}
		if err := b.Group("seed", fmt.Sprintf("g%02d", g), ids, ts); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				now := atomic.AddInt64(&ts, 1)
				g := (worker + k) % groups
				target := fmt.Sprintf("g%02d", g)
				anchor := fmt.Sprintf("e%03d", ((g+1)%groups)*perGroup)
				side := Above
				if k%2 == 0 {
					side = Below
				}
				if err := b.Reorder("seed", target, anchor, side, b.Rev(), now); err != nil {
					continue
				}
			}
		}(w)
	}
	wg.Wait()
	order := b.Order()
	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	for g := 0; g < groups; g++ {
		for j := 1; j < perGroup; j++ {
			a := fmt.Sprintf("e%03d", g*perGroup+j-1)
			c := fmt.Sprintf("e%03d", g*perGroup+j)
			if pos[a] >= pos[c] {
				t.Fatalf("group g%02d internal order broken: %s(%d) !< %s(%d)", g, a, pos[a], c, pos[c])
			}
		}
	}
}
