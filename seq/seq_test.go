package seq

import (
	"errors"
	"sync"
	"testing"

	"ontology/stride"
)

func mustNew(t *testing.T, k int, m int64) *Store {
	t.Helper()
	st, err := New(k, m)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", k, m, err)
	}
	return st
}

func TestNewValidation(t *testing.T) {
	for _, k := range []int{0, 1, 9, -3} {
		if _, err := New(k, 4); !errors.Is(err, ErrParam) {
			t.Errorf("New(k=%d): err=%v, want ErrParam", k, err)
		}
	}
	for _, m := range []int64{0, 1, 2, 17, -4} {
		if _, err := New(3, m); !errors.Is(err, ErrParam) {
			t.Errorf("New(3,m=%d): err=%v, want ErrParam", 3, err)
		}
	}
	if _, err := New(2, 2); err != nil {
		t.Errorf("New(2,2): %v", err)
	}
	if _, err := New(8, 16); err != nil {
		t.Errorf("New(8,16): %v", err)
	}
}

func TestIssueBasic(t *testing.T) {
	st := mustNew(t, 3, 4)
	for want := int64(1); want <= 3; want++ {
		id, err := st.Issue(1)
		if err != nil || id != want {
			t.Fatalf("Issue(1)=%d,%v, want %d", id, err, want)
		}
	}
	if got := st.NextOf(1); got != 4 {
		t.Fatalf("next_1=%d, want 4", got)
	}
	if _, err := st.Issue(2); !errors.Is(err, ErrInactive) {
		t.Fatalf("Issue(2) err=%v, want ErrInactive", err)
	}
	if _, err := st.Issue(0); !errors.Is(err, ErrParam) {
		t.Fatalf("Issue(0) err=%v, want ErrParam", err)
	}
	if _, err := st.Issue(4); !errors.Is(err, ErrParam) {
		t.Fatalf("Issue(4) err=%v, want ErrParam", err)
	}
}

// 例四：Reserve 全成或全不成，最后一个编号恰等于 MaxID 时成功。
func TestReserveMaxIDBoundary(t *testing.T) {
	st := mustNew(t, 3, 4)
	st.setNext(1, stride.MaxID-1)
	ids, err := st.Reserve(1, 2)
	if err != nil {
		t.Fatalf("Reserve(1,2): %v", err)
	}
	if ids[0] != stride.MaxID-1 || ids[1] != stride.MaxID {
		t.Fatalf("ids=%v, want [%d %d]", ids, stride.MaxID-1, stride.MaxID)
	}
	if _, err := st.Issue(1); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Issue after exhaustion err=%v, want ErrExhausted", err)
	}
	before := st.NextOf(1)
	if _, err := st.Reserve(1, 3); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Reserve(1,3) err=%v, want ErrExhausted", err)
	}
	if got := st.NextOf(1); got != before {
		t.Fatalf("被拒后 next_1=%d, want 不变 %d", got, before)
	}
}

func TestReserveValidation(t *testing.T) {
	st := mustNew(t, 3, 4)
	for _, n := range []int{0, -1, 1001} {
		if _, err := st.Reserve(1, n); !errors.Is(err, ErrParam) {
			t.Errorf("Reserve(1,%d) err=%v, want ErrParam", n, err)
		}
	}
	if _, err := st.Reserve(2, 1); !errors.Is(err, ErrInactive) {
		t.Errorf("Reserve(2,1) err=%v, want ErrInactive", err)
	}
	ids, err := st.Reserve(1, 3)
	if err != nil || len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("Reserve(1,3)=%v,%v, want [1 2 3]", ids, err)
	}
	if got := st.NextOf(1); got != 4 {
		t.Fatalf("next_1=%d, want 4", got)
	}
}

// 例三：Observe 在 id 恰等 next、小 1、跨类时的取值（步长 m=4）。
func TestObserveStrideM(t *testing.T) {
	st := mustNew(t, 3, 4)
	st.setNext(1, 12)
	st.setNext(2, 13)
	st.active[2] = true // 活跃数 2，步长 4

	if err := st.Observe(2, 12); err != nil {
		t.Fatal(err)
	}
	if got := st.NextOf(2); got != 13 {
		t.Fatalf("Observe(2,12) 后 next_2=%d, want 13（无变化）", got)
	}
	if got := st.J(); got != 0 {
		t.Fatalf("J=%d, want 0", got)
	}

	if err := st.Observe(2, 13); err != nil {
		t.Fatal(err)
	}
	if got := st.NextOf(2); got != 17 {
		t.Fatalf("Observe(2,13) 后 next_2=%d, want 17", got)
	}
	if got := st.J(); got != 4 {
		t.Fatalf("J=%d, want 4", got)
	}

	if err := st.Observe(2, 20); err != nil {
		t.Fatal(err)
	}
	if got := st.NextOf(2); got != 21 {
		t.Fatalf("Observe(2,20) 后 next_2=%d, want 21", got)
	}
	if got := st.J(); got != 8 {
		t.Fatalf("J=%d, want 8", got)
	}
}

func TestObserveStride1AndRejects(t *testing.T) {
	st := mustNew(t, 3, 4)
	if err := st.Observe(1, 5); err != nil {
		t.Fatal(err)
	}
	if got := st.NextOf(1); got != 6 {
		t.Fatalf("步长1 Observe(1,5) 后 next_1=%d, want 6", got)
	}
	if got := st.J(); got != 5 {
		t.Fatalf("J=%d, want 5", got)
	}
	if err := st.Observe(1, 3); err != nil {
		t.Fatal(err)
	}
	if got := st.NextOf(1); got != 6 {
		t.Fatalf("Observe(1,3) 后 next_1=%d, want 6（无变化）", got)
	}
	if err := st.Observe(2, 10); !errors.Is(err, ErrInactive) {
		t.Fatalf("Observe(2,10) err=%v, want ErrInactive", err)
	}
	for _, id := range []int64{0, -1, stride.MaxID + 1} {
		if err := st.Observe(1, id); !errors.Is(err, ErrParam) {
			t.Fatalf("Observe(1,%d) err=%v, want ErrParam", id, err)
		}
	}
}

// 被拒操作不得改变任何 next、活跃标志与 J。
func TestRejectedOpsKeepState(t *testing.T) {
	st := mustNew(t, 3, 4)
	if _, err := st.Issue(1); err != nil {
		t.Fatal(err)
	}
	snap := func() ([4]int64, [4]bool, int64) {
		var n [4]int64
		var a [4]bool
		for i := 1; i <= 3; i++ {
			n[i], a[i] = st.NextOf(i), st.Active(i)
		}
		return n, a, st.J()
	}
	beforeN, beforeA, beforeJ := snap()

	rejects := []func() error{
		func() error { _, e := st.Issue(0); return e },
		func() error { _, e := st.Issue(2); return e },
		func() error { _, e := st.Reserve(1, 0); return e },
		func() error { _, e := st.Reserve(1, 1001); return e },
		func() error { _, e := st.Reserve(9, 1); return e },
		func() error { return st.Observe(1, 0) },
		func() error { return st.Observe(1, stride.MaxID+1) },
		func() error { return st.Observe(3, 5) },
	}
	for i, op := range rejects {
		if err := op(); err == nil {
			t.Fatalf("第 %d 个操作应被拒绝", i)
		}
		n, a, j := snap()
		if n != beforeN || a != beforeA || j != beforeJ {
			t.Fatalf("第 %d 个被拒操作改变了状态: next=%v active=%v J=%d", i, n, a, j)
		}
	}
}

// 非导出计数器：Issue 与 Reserve 触碰的系统数恰为 1，与 K 无关。
func TestTouchedSystems(t *testing.T) {
	for k := MinK; k <= MaxK; k++ {
		st := mustNew(t, k, int64(k))
		if _, err := st.Issue(1); err != nil {
			t.Fatal(err)
		}
		if st.touched != 1 {
			t.Fatalf("K=%d Issue 触碰系统数=%d, want 1", k, st.touched)
		}
		if _, err := st.Reserve(1, 1000); err != nil {
			t.Fatal(err)
		}
		if st.touched != 1 {
			t.Fatalf("K=%d Reserve 触碰系统数=%d, want 1", k, st.touched)
		}
	}
}

// 并发签发：结果等价于某串行顺序，编号全局唯一且不超过 MaxID。
func TestConcurrentIssueUnique(t *testing.T) {
	st := mustNew(t, 2, 2)
	st.setNext(1, 2) // 类 0
	st.setNext(2, 1) // 类 1
	st.active[2] = true
	const goroutines = 8
	const per = 500
	var wg sync.WaitGroup
	results := make([][]int64, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			sys := g%2 + 1
			ids := make([]int64, 0, per)
			for i := 0; i < per; i++ {
				id, err := st.Issue(sys)
				if err != nil {
					t.Errorf("Issue(%d): %v", sys, err)
					return
				}
				ids = append(ids, id)
			}
			results[g] = ids
		}(g)
	}
	wg.Wait()
	seen := make(map[int64]bool, goroutines*per)
	for _, ids := range results {
		for i, id := range ids {
			if id < 1 || id > stride.MaxID {
				t.Fatalf("id=%d 越界", id)
			}
			if seen[id] {
				t.Fatalf("id=%d 被重复签发", id)
			}
			seen[id] = true
			if i > 0 && id <= ids[i-1] {
				t.Fatalf("同一系统编号未严格递增: %v", ids)
			}
		}
	}
	if len(seen) != goroutines*per {
		t.Fatalf("签发总数=%d, want %d", len(seen), goroutines*per)
	}
}
