package bar

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestRegistryLookupConstant 证明进程登记靠 ID 哈希索引而非整表扫描：
// 无论已登记进程数 m 多大，一次 Arrive 为登记而检查的条目个数是常数。
func TestRegistryLookupConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			b := New(m)
			for i := 0; i < m; i++ {
				if err := b.Arrive(fmt.Sprintf("p%d", i)); err != nil {
					t.Fatalf("arrive p%d: %v", i, err)
				}
			}
			// 登记表已满，新 ID 的 Arrive 触发一次登记查找后被拒。
			if err := b.Arrive("overflow"); !errors.Is(err, ErrRegistryFull) {
				t.Fatalf("got %v, want ErrRegistryFull", err)
			}
			if b.checked > 1 {
				t.Fatalf("m=%d: checked %d entries, grows with m; want O(1)", m, b.checked)
			}
			// 已登记进程的 Arrive 同样只查一次。
			if err := b.Arrive("p0"); !errors.Is(err, ErrDuplicateArrive) {
				t.Fatalf("got %v, want ErrDuplicateArrive", err)
			}
			if b.checked > 1 {
				t.Fatalf("m=%d: checked %d entries for registered id; want O(1)", m, b.checked)
			}
		})
	}
}

// TestConcurrentArriveDepart 并发：N 个 goroutine 并发 Arrive 后释放；
// 并发 Depart 后轮次+1 且回到未释放。用 chan 同步起跑，不用 sleep。
func TestConcurrentArriveDepart(t *testing.T) {
	for _, n := range []int{4, 64, 256} {
		b := New(n)
		for _, op := range []func(string) error{b.Arrive, b.Depart} {
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					<-start
					if err := op(id); err != nil {
						t.Errorf("op %s: %v", id, err)
					}
				}(fmt.Sprintf("p%d", i))
			}
			close(start)
			wg.Wait()
		}
		if b.Round() != 1 || b.Released() {
			t.Fatalf("n=%d: round=%d released=%v, want 1/false", n, b.Round(), b.Released())
		}
	}
}

// TestMultiRoundShuffled 多轮 + 随机顺序：每轮以随机顺序到达、
// 随机顺序离开，轮次严格推进。
func TestMultiRoundShuffled(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	b := New(9)
	for round := 0; round < 20; round++ {
		for _, p := range rng.Perm(9) {
			if err := b.Arrive(fmt.Sprintf("p%d", p)); err != nil {
				t.Fatalf("round %d arrive: %v", round, err)
			}
		}
		for _, p := range rng.Perm(9) {
			if err := b.Depart(fmt.Sprintf("p%d", p)); err != nil {
				t.Fatalf("round %d depart: %v", round, err)
			}
		}
		if b.Round() != round+1 {
			t.Fatalf("round=%d, want %d", b.Round(), round+1)
		}
	}
}
