package api

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

type op struct {
	txid  int64
	key   string
	delta int
}

func snapEq(s *Service, state map[string]int, set []int64) bool {
	st, se := s.Snapshot()
	return reflect.DeepEqual(st, state) && reflect.DeepEqual(se, set)
}

func mustApply(t *testing.T, s *Service, o op) {
	t.Helper()
	if err := s.Apply(o.txid, o.key, o.delta); err != nil {
		t.Fatalf("Apply(%+v): %v", o, err)
	}
}

// TestSixSteps 钉住第三节六步推导：每步之后的 state 与已应用集。
func TestSixSteps(t *testing.T) {
	s := New()
	ops := []op{{1, "k", 5}, {2, "k", 3}, {1, "k", 5}, {3, "k", 5}, {2, "k", 3}, {4, "m", 2}}
	states := []map[string]int{{"k": 5}, {"k": 8}, {"k": 8}, {"k": 13}, {"k": 13}, {"k": 13, "m": 2}}
	sets := [][]int64{{1}, {1, 2}, {1, 2}, {1, 2, 3}, {1, 2, 3}, {1, 2, 3, 4}}
	for i := range ops {
		mustApply(t, s, ops[i])
		if !snapEq(s, states[i], sets[i]) {
			st, se := s.Snapshot()
			t.Fatalf("步 %d 后 state=%v set=%v", i+1, st, se)
		}
	}
}

// TestIdempotentRepeat 同一 txid 重复 Apply（含内容不同）前后状态完全不变。
func TestIdempotentRepeat(t *testing.T) {
	s := New()
	mustApply(t, s, op{1, "k", 5})
	before, beforeSet := s.Snapshot()
	for _, dup := range []op{{1, "k", 5}, {1, "k", 99}, {1, "other", 7}} {
		mustApply(t, s, dup)
		if !snapEq(s, before, beforeSet) {
			t.Fatalf("重复 %+v 后状态改变", dup)
		}
	}
}

// TestRejectionNoTrace 三类哨兵错误互不相同，被拒后不留痕、可继续用。
func TestRejectionNoTrace(t *testing.T) {
	s := New()
	mustApply(t, s, op{1, "k", 5})
	before, beforeSet := s.Snapshot()
	for _, c := range []struct {
		op  op
		err error
	}{
		{op{0, "k", 1}, ErrInvalidTxID},
		{op{-3, "k", 1}, ErrInvalidTxID},
		{op{2, "", 1}, ErrEmptyKey},
		{op{2, "k", 0}, ErrZeroDelta},
	} {
		if err := s.Apply(c.op.txid, c.op.key, c.op.delta); !errors.Is(err, c.err) {
			t.Fatalf("%+v 应报 %v, got %v", c.op, c.err, err)
		}
	}
	if ErrInvalidTxID == ErrEmptyKey || ErrEmptyKey == ErrZeroDelta || ErrInvalidTxID == ErrZeroDelta {
		t.Fatal("三类哨兵错误必须互不相同")
	}
	if err := s.Restore([]int64{1, 0}); !errors.Is(err, ErrInvalidTxID) {
		t.Fatalf("Restore 含非法 txid 应报 ErrInvalidTxID, got %v", err)
	}
	if !snapEq(s, before, beforeSet) {
		t.Fatal("被拒操作留痕")
	}
	mustApply(t, s, op{2, "k", 1}) // 拒绝后仍可正常使用
}

// TestRestoreSkipsRedelivered 恢复去重日志后，这些 txid 的重复到达仍被跳过。
func TestRestoreSkipsRedelivered(t *testing.T) {
	s := New()
	if err := s.Restore([]int64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	mustApply(t, s, op{2, "k", 5}) // 已恢复 → 跳过
	if !snapEq(s, map[string]int{}, []int64{1, 2, 3}) {
		t.Fatal("恢复后重复到达未被跳过")
	}
	mustApply(t, s, op{4, "k", 5}) // 新 txid 正常应用
	if !snapEq(s, map[string]int{"k": 5}, []int64{1, 2, 3, 4}) {
		t.Fatal("恢复后新 txid 未正常应用")
	}
}

// TestNaiveReference 伪随机序列与朴素参照逐 Key 一致，已应用集等于所有出现过的 txid。
func TestNaiveReference(t *testing.T) {
	s := New()
	naive := map[string]int{}
	seen := map[int64]bool{}
	seed := int64(42)
	for i := 0; i < 500; i++ {
		seed = (seed*1103515245 + 12345) & (1<<62 - 1)
		o := op{seed%17 + 1, string(rune('a' + seed%5)), int(seed%9 + 1)}
		mustApply(t, s, o)
		if !seen[o.txid] {
			seen[o.txid] = true
			naive[o.key] += o.delta
		}
	}
	state, set := s.Snapshot()
	if !reflect.DeepEqual(state, naive) || len(set) != len(seen) {
		t.Fatalf("state=%v want %v; |set|=%d want %d", state, naive, len(set), len(seen))
	}
}

// TestConcurrent 并发不同 txid 等于朴素参照；并发同一 txid 恰好应用一次。
func TestConcurrent(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		distinct, same := New(), New()
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(2)
			go func(i int) { defer wg.Done(); _ = distinct.Apply(int64(i+1), "k", i+1) }(i)
			go func() { defer wg.Done(); _ = same.Apply(7, "k", 2) }()
		}
		wg.Wait()
		st, set := distinct.Snapshot()
		if st["k"] != n*(n+1)/2 || len(set) != n {
			t.Fatalf("n=%d: state[k]=%d want %d, |set|=%d", n, st["k"], n*(n+1)/2, len(set))
		}
		if !snapEq(same, map[string]int{"k": 2}, []int64{7}) {
			t.Fatalf("n=%d: 并发同一 txid 未恰好应用一次", n)
		}
	}
}

// TestSelfCheck 自检方法本身必须通过。
func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
