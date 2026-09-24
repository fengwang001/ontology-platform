package api

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func eqBlocks(a, b []int) bool { return len(a) == len(b) && (len(a) == 0 || reflect.DeepEqual(a, b)) }

// TestTwelveSteps 第三节十二步：逐步核对 M、溢写块号与最终输出。
func TestTwelveSteps(t *testing.T) {
	a, err := New(4, 2)
	must(t, err)
	for tx := 1; tx <= 3; tx++ {
		must(t, a.Begin(tx))
	}
	steps := []struct {
		op, row string
		tx, m   int
		blocks  []int
	}{
		{"A", "a1", 1, 1, nil}, {"A", "b1", 2, 2, nil}, {"A", "b2", 2, 3, nil},
		{"A", "a2", 1, 4, nil}, {"A", "c1", 3, 3, []int{0}}, {"A", "a3", 1, 4, []int{0}},
		{"A", "c2", 3, 3, []int{0, 1}}, {"R", "", 2, 3, []int{0}}, {"A", "a4", 1, 4, []int{0}},
		{"A", "a5", 1, 2, []int{0, 2}}, {"A", "a6", 1, 3, []int{0, 2}}, {"C", "", 1, 2, nil},
	}
	for i, s := range steps {
		switch s.op {
		case "A":
			must(t, a.Append(s.tx, s.row))
		case "C":
			must(t, a.Commit(s.tx))
		case "R":
			must(t, a.Rollback(s.tx))
		}
		if a.b.M() != s.m || !eqBlocks(a.SpillBlocks(), s.blocks) {
			t.Fatalf("step %d: M=%d blocks=%v, want M=%d blocks=%v",
				i+1, a.b.M(), a.SpillBlocks(), s.m, s.blocks)
		}
	}
	if want := []string{"a1", "a2", "a3", "a4", "a5", "a6"}; !reflect.DeepEqual(a.Log(), want) {
		t.Fatalf("log=%v, want %v", a.Log(), want)
	}
}

// TestCommitReplayOrder 提交时块号升序回放、再内存行；M==limit 不溢写。
func TestCommitReplayOrder(t *testing.T) {
	a, _ := New(2, 5)
	must(t, a.Begin(1))
	for _, r := range []string{"x1", "x2", "x3", "x4", "x5"} { // 第 3 行触发整块溢写 #0
		must(t, a.Append(1, r))
	}
	if got := a.SpillBlocks(); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("blocks=%v", got)
	}
	must(t, a.Commit(1))
	if want := []string{"x1", "x2", "x3", "x4", "x5"}; !reflect.DeepEqual(a.Log(), want) {
		t.Fatalf("log=%v", a.Log())
	}
}

// TestNaiveEquivalence 多档参数随机序列对拍朴素参照（I1、I3）。
func TestNaiveEquivalence(t *testing.T) {
	for _, tc := range []struct{ l, s, seed, n int }{
		{4, 2, 1, 2000}, {1, 1, 7, 1000}, {9, 4, 3, 3000}, {3, 2, 11, 5000},
	} {
		if err := vsNaive(tc.l, tc.s, tc.seed, tc.n); err != nil {
			t.Errorf("limit=%d spill=%d: %v", tc.l, tc.s, err)
		}
	}
}

// TestNoResidue 回滚 tx2 后提交 tx1：tx1 行序正确、tx2 的行与块均无残留。
func TestNoResidue(t *testing.T) {
	a, _ := New(2, 6)
	must(t, a.Begin(1))
	must(t, a.Begin(2))
	for i := 0; i < 6; i++ { // 两个事务都产生溢写块
		must(t, a.Append(1, fmt.Sprintf("a%d", i)))
		must(t, a.Append(2, fmt.Sprintf("b%d", i)))
	}
	must(t, a.Rollback(2))
	must(t, a.Commit(1))
	if want := []string{"a0", "a1", "a2", "a3", "a4", "a5"}; !reflect.DeepEqual(a.Log(), want) || len(a.SpillBlocks()) != 0 {
		t.Fatalf("log=%v blocks=%v", a.Log(), a.SpillBlocks())
	}
}

// TestRejectedLeavesState 四类错误可判定、互不相同，被拒后状态不变（I4）。
func TestRejectedLeavesState(t *testing.T) {
	if err := rejectNoTrace(); err != nil {
		t.Fatal(err)
	}
	if ErrParam == ErrNoTx || ErrParam == ErrDupTx || ErrParam == ErrSpillFull ||
		ErrNoTx == ErrDupTx || ErrNoTx == ErrSpillFull || ErrDupTx == ErrSpillFull {
		t.Fatalf("四类哨兵错误不互异")
	}
}

// TestConcurrent 并发 Begin/Append/Commit：各事务行连续有序，存储最终为空。
func TestConcurrent(t *testing.T) {
	const n, rows = 64, 20
	a, err := New(3, 2*n*rows) // limit 小频繁溢写，maxSpill 足够大
	must(t, err)
	var wg sync.WaitGroup
	for tx := 1; tx <= n; tx++ {
		must(t, a.Begin(tx))
		wg.Add(1)
		go func(tx int) {
			defer wg.Done()
			var e error
			for r := 0; r < rows && e == nil; r++ {
				e = a.Append(tx, fmt.Sprintf("t%02d-r%02d", tx, r))
			}
			if e == nil {
				e = a.Commit(tx)
			}
			if e != nil {
				t.Error(e)
			}
		}(tx)
	}
	wg.Wait()
	log := a.Log()
	if len(a.SpillBlocks()) != 0 || len(log) != n*rows {
		t.Fatalf("blocks=%v logLen=%d", a.SpillBlocks(), len(log))
	}
	seen := map[string]bool{} // 每个事务只能在日志里出现一段
	for i := 0; i < len(log); {
		tx := log[i][:3] // 行格式 t%02d-r%02d
		if seen[tx] {
			t.Fatalf("事务 %s 的行不连续 @%d", tx, i)
		}
		seen[tx] = true
		for j := 0; j < rows; j++ { // 段内逐行有序
			if want := fmt.Sprintf("%s-r%02d", tx, j); log[i+j] != want {
				t.Fatalf("日志[%d]=%s, want %s", i+j, log[i+j], want)
			}
		}
		i += rows
	}
}
