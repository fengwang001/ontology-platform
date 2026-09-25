package apply_test

import (
	"errors"
	"ontology/apply"
	"reflect"
	"sort"
	"sync"
	"testing"
)

type op struct {
	txid  int64
	key   string
	delta int
}

func ap(t *testing.T, e *apply.Engine, o op) bool {
	t.Helper()
	a, err := e.Apply(o.txid, o.key, o.delta)
	if err != nil {
		t.Fatalf("apply %+v: %v", o, err)
	}
	return a
}

func snapEq(t *testing.T, e *apply.Engine, st map[string]int, set []int64) {
	t.Helper()
	got, gs := e.Snapshot()
	if !reflect.DeepEqual(got, st) || !reflect.DeepEqual(gs, set) {
		t.Fatalf("got %v/%v want %v/%v", got, gs, st, set)
	}
}

// TestNaiveReference 不变量2：与朴素参照（首见 txid 才应用）逐 Key、逐集合一致。
func TestNaiveReference(t *testing.T) {
	cases := [][]op{
		{{1, "k", 5}, {2, "k", 3}, {1, "k", 5}, {3, "k", 5}, {2, "k", 3}, {4, "m", 2}},
		{{7, "a", 10}, {7, "a", -99}, {8, "a", -4}, {9, "b", 1}, {8, "a", 100}},
	}
	for ci, ops := range cases {
		e := apply.New()
		naive, seen := map[string]int{}, map[int64]bool{}
		var txids []int64
		for _, o := range ops {
			ap(t, e, o)
			if !seen[o.txid] {
				seen[o.txid] = true
				naive[o.key] += o.delta
				txids = append(txids, o.txid)
			}
		}
		sort.Slice(txids, func(i, j int) bool { return txids[i] < txids[j] })
		if st, set := e.Snapshot(); !reflect.DeepEqual(st, naive) || !reflect.DeepEqual(set, txids) {
			t.Fatalf("case %d: %v/%v want %v/%v", ci, st, set, naive, txids)
		}
	}
}

// TestNoDuplicateApply 不变量1：任一 txid 至多应用一次，重复不改 state。
func TestNoDuplicateApply(t *testing.T) {
	e := apply.New()
	for i := 0; i < 5; i++ {
		if a := ap(t, e, op{1, "k", 3}); a != (i == 0) {
			t.Fatalf("i=%d applied=%v", i, a)
		}
	}
	snapEq(t, e, map[string]int{"k": 3}, []int64{1})
}

// TestIdempotentRepeat 不变量3：重复到达即便内容不同（含空 key/零 delta）也不改状态。
func TestIdempotentRepeat(t *testing.T) {
	e := apply.New()
	ap(t, e, op{1, "k", 5})
	ap(t, e, op{2, "m", 2})
	st, set := e.Snapshot()
	for _, o := range []op{{1, "k", 5}, {1, "z", 99}, {2, "m", 2}, {1, "", 0}} {
		if ap(t, e, o) {
			t.Fatalf("duplicate %+v applied", o)
		}
	}
	snapEq(t, e, st, set)
}

// TestRejectedLeavesNoTrace 不变量4：三类哨兵互不相同，拒绝不留痕、之后可继续使用。
func TestRejectedLeavesNoTrace(t *testing.T) {
	e := apply.New()
	ap(t, e, op{1, "k", 5})
	var s1, s2, s3 error = apply.ErrInvalidTxID, apply.ErrEmptyKey, apply.ErrZeroDelta
	if s1 == s2 || s2 == s3 || s1 == s3 {
		t.Fatal("sentinel errors not distinct")
	}
	st, set := e.Snapshot()
	bad := []op{{0, "k", 1}, {-3, "k", 1}, {9, "", 1}, {9, "k", 0}}
	want := []error{apply.ErrInvalidTxID, apply.ErrInvalidTxID, apply.ErrEmptyKey, apply.ErrZeroDelta}
	for i, o := range bad {
		if a, err := e.Apply(o.txid, o.key, o.delta); a || !errors.Is(err, want[i]) {
			t.Fatalf("%+v: applied=%v err=%v", o, a, err)
		}
	}
	snapEq(t, e, st, set)
	if !ap(t, e, op{2, "k", 1}) {
		t.Fatal("engine unusable after rejection")
	}
}

// TestRestoreRejectsIllegal 不变量4：非法恢复整体失败、旧集不变；恢复后重投跳过。
func TestRestoreRejectsIllegal(t *testing.T) {
	for _, bad := range [][]int64{{0}, {-1, 2}, {1, 0, 3}, {1, -2}} {
		e := apply.New()
		ap(t, e, op{1, "k", 5})
		st, set := e.Snapshot()
		if err := e.Restore(bad); !errors.Is(err, apply.ErrInvalidRestore) {
			t.Fatalf("%v: %v", bad, err)
		}
		snapEq(t, e, st, set)
	}
	e := apply.New()
	if err := e.Restore([]int64{2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if ap(t, e, op{2, "k", 9}) {
		t.Fatal("restored txid reapplied")
	}
}

// TestConcurrentApply 一条并发测试两阶段：互异 txid 须同朴素参照；同一 txid 同投
// 时 s==5 且集合仅多 1 个即证明恰好一次。多档 N 循环，不用 sleep 造时序。
func TestConcurrentApply(t *testing.T) {
	for _, n := range []int{16, 64, 256} {
		e := apply.New()
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func(i int) { defer wg.Done(); _, _ = e.Apply(int64(100+i), "d", 1) }(i)
		}
		wg.Wait()
		if st, set := e.Snapshot(); st["d"] != n || len(set) != n {
			t.Fatalf("distinct n=%d: d=%d setLen=%d", n, st["d"], len(set))
		}
		wg.Add(n)
		for range n {
			go func() { defer wg.Done(); _, _ = e.Apply(999, "s", 5) }()
		}
		wg.Wait()
		if st, set := e.Snapshot(); st["s"] != 5 || len(set) != n+1 {
			t.Fatalf("same n=%d: s=%d setLen=%d", n, st["s"], len(set))
		}
	}
}
