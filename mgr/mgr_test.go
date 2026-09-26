package mgr

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/lease"
)

// 不变量2：token 严格递增，旧 token 一律被栅栏。
func TestFencingMonotonic(t *testing.T) {
	m := New(10)
	t1, _ := m.Acquire("L", "A", 0)
	t2, _ := m.Acquire("L", "B", 5)
	if t1 != 1 || t2 != 2 {
		t.Fatalf("token 未严格递增: %d,%d", t1, t2)
	}
	if err := m.Renew("L", t1, 6); !errors.Is(err, lease.ErrStaleToken) {
		t.Fatalf("旧 token 未被栅栏: %v", err)
	}
	if _, tok, _, _ := m.Lookup("L"); tok != 2 {
		t.Fatalf("被拒后 token 变了: %d", tok)
	}
}

// 不变量3：续期只在存活期，到期必须重新 Acquire。
func TestRenewOnlyWhileAlive(t *testing.T) {
	m := New(10)
	m.Acquire("L", "A", 0) // expiry=10
	if err := m.Renew("L", 1, 9); err != nil {
		t.Fatalf("存活期内续期被拒: %v", err)
	} // expiry=19
	if err := m.Renew("L", 1, 19); !errors.Is(err, lease.ErrExpired) {
		t.Fatalf("到期续期未拒绝: %v", err)
	}
	if _, _, e, _ := m.Lookup("L"); e != 19 {
		t.Fatalf("被拒后 expiry 变了: %d", e)
	}
}

// 复杂度约束：checked 不随 m 线性增长，不超过 到期数+小常数。
func TestExpiredAllScansBounded(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		m := New(10)
		for i := 0; i < n; i++ {
			m.Acquire(fmt.Sprintf("n%d", i), "o", i) // expiry=i+10 错开
		}
		got := m.ExpiredAll(14) // 恰好 5 个到期（i=0..4）
		if len(got) != 5 {
			t.Fatalf("n=%d 到期数=%d, want 5", n, len(got))
		}
		if m.checked > len(got)+2 {
			t.Fatalf("n=%d checked=%d 超过 到期数+常数", n, m.checked)
		}
	}
}

// 懒删除：续期留下的旧堆项不影响 ExpiredAll 正确性。
func TestExpiredAllWithRenews(t *testing.T) {
	m := New(10)
	m.Acquire("a", "o", 0)
	m.Acquire("b", "o", 0)
	m.Renew("a", 1, 5) // a 的 expiry=15，堆里留旧项 a@10
	got := m.ExpiredAll(12)
	if len(got) != 1 || got[0] != "b" {
		t.Fatalf("ExpiredAll=%v, want [b]", got)
	}
}

// 并发：多 goroutine 各自 Acquire/Renew，读到的 expiry 单调不减，结束后值正确。
func TestConcurrentRenewMonotonic(t *testing.T) {
	const M, Rounds, TTL = 8, 200, 100000
	m := New(TTL)
	for g := 0; g < M; g++ {
		m.Acquire(fmt.Sprintf("w%d", g), "o", 0)
	}
	var wg, rwg sync.WaitGroup
	done := make(chan struct{})
	var monoBad atomic.Bool
	rwg.Add(1)
	go func() { // 读者：不打断写者，循环读 w0 的 expiry
		defer rwg.Done()
		prev := -1
		for {
			select {
			case <-done:
				return
			default:
				if _, _, e, err := m.Lookup("w0"); err == nil {
					if e < prev {
						monoBad.Store(true)
					}
					prev = e
				}
			}
		}
	}()
	for g := 0; g < M; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			name := fmt.Sprintf("w%d", g)
			for now := 1; now <= Rounds; now++ {
				if err := m.Renew(name, 1, now); err != nil {
					t.Errorf("Renew(%s)=%v", name, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(done)
	rwg.Wait()
	if monoBad.Load() {
		t.Fatal("并发读到的 expiry 非单调不减")
	}
	for g := 0; g < M; g++ {
		if _, _, e, _ := m.Lookup(fmt.Sprintf("w%d", g)); e != Rounds+TTL {
			t.Fatalf("w%d expiry=%d, want %d", g, e, Rounds+TTL)
		}
	}
}
