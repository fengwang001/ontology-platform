package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/txid"
)

// 要求 8：遍历全部崩溃点（提交调用序列 0,1,2,2,3,4），恢复后
// 要么全可见要么全不可见，不允许半可见，且无残留。
func TestCrashPoints(t *testing.T) {
	for crashAt := 0; crashAt < 6; crashAt++ {
		t.Run(fmt.Sprintf("point%d", crashAt), func(t *testing.T) {
			s := New(txid.NewCounter(), Config{})
			calls := 0
			s.SetCrashHook(func(int) {
				if calls == crashAt {
					panic("crash")
				}
				calls++
			})
			tx := s.Begin()
			_ = tx.Put("c1", []byte("x"))
			_ = tx.Put("c2", []byte("y"))
			func() {
				defer func() { recover() }()
				_ = tx.Commit()
			}()
			s.Recover()
			s.SetCrashHook(nil)
			v, _ := s.BeginView()
			defer v.Close()
			_, e1 := v.Get("c1")
			_, e2 := v.Get("c2")
			if (e1 == nil) != (e2 == nil) {
				t.Fatalf("half-visible: %v / %v", e1, e2)
			}
			if visible := e1 == nil; visible != (crashAt == 5) {
				t.Fatalf("visible=%v at point %d", visible, crashAt)
			}
			want := 0
			if crashAt == 5 {
				want = 2
			}
			if got := s.Stats("c1").TotalVersions; got != want {
				t.Fatalf("total=%d want %d", got, want)
			}
			put(t, s, "c1", "after") // 恢复后存储仍可用
		})
	}
}

// 要求 9：三类超限彼此可判定，且拒绝后状态零变化。
func TestLimits(t *testing.T) {
	sentinels := []error{ErrChainTooLong, ErrTooManySnapshots, ErrTooManyVersions}
	check := func(t *testing.T, err, want error) {
		t.Helper()
		for _, e := range sentinels {
			if errors.Is(err, e) != (e == want) {
				t.Fatalf("err=%v not distinguishable (want %v)", err, want)
			}
		}
	}
	t.Run("chain too long", func(t *testing.T) {
		s := New(txid.NewCounter(), Config{MaxVersionsPerKey: 1})
		put(t, s, "k", "v1")
		before := s.Stats("k")
		tx := s.Begin()
		_ = tx.Put("k", []byte("v2"))
		check(t, tx.Commit(), ErrChainTooLong)
		if after := s.Stats("k"); after != before {
			t.Fatalf("state changed: %+v -> %+v", before, after)
		}
	})
	t.Run("too many versions", func(t *testing.T) {
		s := New(txid.NewCounter(), Config{MaxTotalVersions: 1})
		put(t, s, "k1", "v")
		before := s.Stats("k2")
		tx := s.Begin()
		_ = tx.Put("k2", []byte("v"))
		check(t, tx.Commit(), ErrTooManyVersions)
		if after := s.Stats("k2"); after != before {
			t.Fatalf("state changed: %+v -> %+v", before, after)
		}
	})
	t.Run("too many snapshots", func(t *testing.T) {
		s := New(txid.NewCounter(), Config{MaxSnapshots: 1})
		v1, _ := s.BeginView()
		defer v1.Close()
		_, err := s.BeginView()
		check(t, err, ErrTooManySnapshots)
		if s.Stats("").Snapshots != 1 {
			t.Fatal("snapshot count changed")
		}
	})
}

// 要求 11：并发写同键、并发开关快照、并发回收；长事务不阻塞其他键。
func TestConcurrent(t *testing.T) {
	s := New(txid.NewCounter(), Config{})
	put(t, s, "hot", "0")
	long := s.Begin()
	_ = long.Put("blocker", []byte("x"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for i := 0; i < 20; i++ {
					tx := s.Begin()
					_ = tx.Put("hot", []byte(fmt.Sprintf("%d-%d", id, i)))
					_ = tx.Commit()
					v, _ := s.BeginView()
					_, _ = v.Get("hot")
					v.Close()
				}
			}(w)
		}
		wg.Wait()
		s.Collect()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("long transaction blocked other keys")
	}
	_ = long.Rollback()
}
