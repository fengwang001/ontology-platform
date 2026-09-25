package api_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/txn"
)

func rc(s int, k string, v int64) api.Rec { return api.Rec{Seq: s, Key: k, Val: v} }
func eq(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestEightSteps 逐步核验八步：新追加条数、变化的 Key、幂等跳过与整批拒绝。
func TestEightSteps(t *testing.T) {
	s := api.New()
	cs := []struct {
		tx string
		rs []api.Rec
		n  int
		ch map[string]int64
		e  error
	}{
		{"t1", []api.Rec{rc(0, "a", 5), rc(1, "b", 3), rc(2, "c", 7)}, 3, map[string]int64{"a": 5, "b": 3, "c": 7}, nil},
		{"t2", []api.Rec{rc(0, "a", 9), rc(1, "d", 2)}, 2, map[string]int64{"a": 9, "d": 2}, nil},
		{"t1", []api.Rec{rc(3, "e", 4)}, 1, map[string]int64{"e": 4}, nil},
		{"t1", []api.Rec{rc(0, "a", 5)}, 0, nil, nil},
		{"t1", []api.Rec{rc(1, "b", 99)}, 0, nil, nil},
		{"t3", []api.Rec{rc(0, "f", 1), rc(1, "", 2)}, 0, nil, txn.ErrEmptyKey},
		{"t2", []api.Rec{rc(2, "a", 11)}, 1, map[string]int64{"a": 11}, nil},
		{"t2", []api.Rec{rc(2, "a", 99)}, 0, nil, nil},
	}
	for i, c := range cs {
		before := s.View()
		n, err := s.Commit(c.tx, c.rs)
		if n != c.n || !errors.Is(err, c.e) {
			t.Fatalf("step %d: n=%d err=%v want n=%d err=%v", i+1, n, err, c.n, c.e)
		}
		for k, v := range c.ch {
			if s.View()[k] != v {
				t.Fatalf("step %d: %s=%d want %d", i+1, k, s.View()[k], v)
			}
		}
		if c.ch == nil && !eq(s.View(), before) {
			t.Fatalf("step %d view changed: %v", i+1, s.View())
		}
	}
	if !eq(s.View(), map[string]int64{"a": 11, "b": 3, "c": 7, "d": 2, "e": 4}) {
		t.Fatalf("final=%v", s.View())
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
func TestReplayIdempotent(t *testing.T) {
	s := api.New()
	if n, err := s.Commit("p", []api.Rec{rc(0, "a", 1), rc(1, "b", 2)}); err != nil || n != 2 {
		t.Fatal(err)
	}
	before := s.View()
	for i, r := range []api.Rec{rc(0, "a", 1), rc(1, "b", 2), rc(0, "a", 999)} {
		if n, err := s.Commit("p", []api.Rec{r}); err != nil || n != 0 || !eq(s.View(), before) {
			t.Fatalf("replay %d n=%d err=%v view=%v", i, n, err, s.View())
		}
	}
}

// TestRejectedBatchLeavesNoTrace 钉住不变量 4 与三类哨兵：可判定、互不相同、整批不留痕、之后仍可用。
func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	bad := []struct {
		tx string
		rs []api.Rec
		we error
	}{
		{"", []api.Rec{rc(0, "a", 1)}, txn.ErrEmptyTxID},
		{"q", []api.Rec{rc(0, "", 1)}, txn.ErrEmptyKey},
		{"q", []api.Rec{rc(1, "a", 1), rc(1, "b", 2)}, txn.ErrDuplicateSeq},
	}
	if txn.ErrEmptyTxID == txn.ErrEmptyKey || txn.ErrEmptyKey == txn.ErrDuplicateSeq || txn.ErrEmptyTxID == txn.ErrDuplicateSeq {
		t.Fatal("sentinel errors must be distinct")
	}
	for i, b := range bad {
		s := api.New()
		s.Commit("q", []api.Rec{rc(0, "seed", 7)})
		pre := s.View()
		if n, err := s.Commit(b.tx, b.rs); n != 0 || !errors.Is(err, b.we) || !eq(s.View(), pre) {
			t.Fatalf("case %d: n=%d err=%v view=%v", i, n, err, s.View())
		}
		if _, err := s.Commit("q", []api.Rec{rc(i+1, "ok", 1)}); err != nil {
			t.Fatalf("case %d unusable after reject: %v", i, err)
		}
	}
}

// TestConcurrentCommit 并发提交不同 txID：结束后等于批量重算；视图大小恒为 per 整倍，无撕裂。
func TestConcurrentCommit(t *testing.T) {
	s := api.New()
	const N, per = 16, 64
	var wg, rg sync.WaitGroup
	var writing atomic.Bool
	writing.Store(true)
	for r := 0; r < 4; r++ {
		rg.Add(1)
		go func() {
			defer rg.Done()
			for writing.Load() {
				v := s.View()        // 底层竞争由 -race 钉
				if len(v)%per != 0 { // 每批原子地恰增 per 个 key
					t.Errorf("torn view: %d keys not multiple of %d", len(v), per)
				}
			}
		}()
	}
	expect := map[string]int64{}
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			b := make([]api.Rec, per)
			for i := range b {
				b[i] = rc(i, fmt.Sprintf("g%d-k%d", g, i), int64(g*per+i))
			}
			if n, err := s.Commit(fmt.Sprintf("tx-%d", g), b); err != nil || n != per {
				t.Errorf("g=%d n=%d err=%v", g, n, err)
			}
		}(g)
		for i := 0; i < per; i++ {
			expect[fmt.Sprintf("g%d-k%d", g, i)] = int64(g*per + i)
		}
	}
	wg.Wait()
	writing.Store(false)
	rg.Wait()
	if !eq(s.View(), expect) {
		t.Fatalf("concurrent view != recompute: %d vs %d keys", len(s.View()), len(expect))
	}
}
