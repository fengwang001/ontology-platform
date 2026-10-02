package epaxos

import (
	"errors"
	"reflect"
	"testing"
)

func inst(r, i int) Instance { return Instance{R: r, I: i} }

func mustNew(t *testing.T, n, cap int) *Executor {
	t.Helper()
	e, err := NewExecutor(n, cap)
	if err != nil {
		t.Fatalf("NewExecutor(%d, %d): %v", n, cap, err)
	}
	return e
}

func mustCommit(t *testing.T, e *Executor, in Instance, seq int, deps ...Instance) {
	t.Helper()
	if err := e.Commit(in, seq, deps); err != nil {
		t.Fatalf("Commit(%v, %d, %v): %v", in, seq, deps, err)
	}
}

func checkExecute(t *testing.T, e *Executor, want []Instance) {
	t.Helper()
	got := e.Execute()
	if len(got) == 0 && len(want) == 0 {
		t.Logf("Execute() = [] (无可执行实例)")
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Execute() = %v, want %v", got, want)
	}
	t.Logf("Execute() = %v", got)
}

// 两实例互相依赖构成一个分量，分量内按 seq 升序：B(3) 先于 A(5)。
func TestMutualDependencyOrderedBySeq(t *testing.T) {
	e := mustNew(t, 2, 8)
	a, b := inst(0, 1), inst(1, 1)
	mustCommit(t, e, a, 5, b)
	mustCommit(t, e, b, 3, a)
	t.Logf("输入: A=%v seq=5 deps=[B], B=%v seq=3 deps=[A]; 判定依据: 同一 SCC 内按 (seq,r,i) 升序", a, b)
	checkExecute(t, e, []Instance{b, a})
	if got := e.Pending(); len(got) != 0 {
		t.Fatalf("Pending() = %v, want empty", got)
	}
}

// 依赖者 seq 更小时仍先输出被依赖者（分量间被依赖者优先）。
func TestDependentWithSmallerSeqStillAfterDep(t *testing.T) {
	e := mustNew(t, 2, 8)
	a, b := inst(0, 1), inst(1, 1)
	mustCommit(t, e, a, 1, b)
	mustCommit(t, e, b, 9)
	t.Logf("输入: A=%v seq=1 deps=[B], B=%v seq=9; 判定依据: A 依赖 B，B 的分量必须先输出", a, b)
	checkExecute(t, e, []Instance{b, a})
}

// 依赖未提交实例会阻塞自身及传递依赖者，提交后下一次 Execute 释放。
func TestUncommittedDepBlocksAndReleases(t *testing.T) {
	e := mustNew(t, 2, 8)
	a, b, c := inst(0, 1), inst(1, 1), inst(0, 2)
	mustCommit(t, e, a, 1, b) // A 依赖未提交的 B
	mustCommit(t, e, c, 2, a) // C 依赖 A，传递阻塞
	t.Logf("输入: A deps=[B(未提交)], C deps=[A]; 判定依据: 可达闭包内含未提交实例则阻塞")
	checkExecute(t, e, nil)
	if got, want := e.Pending(), []Instance{a, c}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Pending() = %v, want %v", got, want)
	}
	mustCommit(t, e, b, 7)
	t.Logf("提交 B 后: 判定依据: 链式分量 B<-A<-C，被依赖者先输出")
	checkExecute(t, e, []Instance{b, a, c})
}

// 就绪分量并列时取首个成员（按 seq,r,i）最小者先输出。
func TestReadyComponentsSmallestHeadFirst(t *testing.T) {
	e := mustNew(t, 2, 8)
	x, y := inst(0, 1), inst(1, 1)
	mustCommit(t, e, x, 5)
	mustCommit(t, e, y, 2)
	t.Logf("输入: X seq=5, Y seq=2 互不依赖; 判定依据: 就绪分量首成员最小者(Y)先输出")
	checkExecute(t, e, []Instance{y, x})

	// seq 相同则按 (r,i) 比较首成员。
	e2 := mustNew(t, 2, 8)
	mustCommit(t, e2, inst(1, 1), 5)
	mustCommit(t, e2, inst(0, 1), 5)
	t.Logf("输入: (1,1) seq=5, (0,1) seq=5; 判定依据: seq 相同首成员按 r 升序，(0,1) 先")
	checkExecute(t, e2, []Instance{inst(0, 1), inst(1, 1)})
}

// 分量内 seq 相同按 r 再按 i 升序。
func TestSameSeqWithinComponentByReplicaThenSlot(t *testing.T) {
	e := mustNew(t, 2, 8)
	x, y, z := inst(0, 1), inst(1, 1), inst(0, 2)
	mustCommit(t, e, x, 7, y)
	mustCommit(t, e, y, 7, z)
	mustCommit(t, e, z, 7, x)
	t.Logf("输入: 三环 X->Y->Z->X seq 均为 7; 判定依据: 分量内 (seq,r,i) 升序 => (0,1),(0,2),(1,1)")
	checkExecute(t, e, []Instance{x, z, y})
}

// 依赖已执行实例视为已满足，不构成边也不阻塞。
func TestExecutedDepIsSatisfied(t *testing.T) {
	e := mustNew(t, 2, 8)
	a, b := inst(0, 1), inst(1, 1)
	mustCommit(t, e, a, 1)
	checkExecute(t, e, []Instance{a})
	mustCommit(t, e, b, 1, a)
	t.Logf("输入: B deps=[A(已执行)]; 判定依据: 已执行依赖已满足，B 立即可执行")
	checkExecute(t, e, []Instance{b})
}

// 三实例环外挂一个依赖者：环分量先整体输出，外挂依赖者随后。
func TestCycleWithExternalDependent(t *testing.T) {
	e := mustNew(t, 2, 8)
	a, b, c, d := inst(0, 1), inst(1, 1), inst(0, 2), inst(1, 2)
	mustCommit(t, e, a, 3, b)
	mustCommit(t, e, b, 1, c)
	mustCommit(t, e, c, 2, a)
	mustCommit(t, e, d, 1, a)
	t.Logf("输入: 环 A->B->C->A (seq 3,1,2)，D deps=[A]; 判定依据: 环分量内按 seq 升序 B,C,A；D 依赖环分量故最后")
	checkExecute(t, e, []Instance{b, c, a, d})
}

// 重复 Commit：seq 与依赖集合（忽略顺序）相同是合法空操作，否则冲突。
func TestDuplicateCommitNoOpAndConflict(t *testing.T) {
	e := mustNew(t, 2, 8)
	a, b, c := inst(0, 1), inst(1, 1), inst(0, 2)
	mustCommit(t, e, a, 3, b, c)
	if err := e.Commit(a, 3, []Instance{c, b}); err != nil { // 依赖顺序不同仍是空操作
		t.Fatalf("identical re-commit: %v", err)
	}
	if err := e.Commit(a, 4, []Instance{b, c}); !errors.Is(err, ErrConflict) {
		t.Fatalf("seq conflict: got %v", err)
	}
	if err := e.Commit(a, 3, []Instance{b}); !errors.Is(err, ErrConflict) {
		t.Fatalf("deps conflict: got %v", err)
	}
	checkExecute(t, e, nil) // a 依赖未提交的 b、c，仍阻塞
	mustCommit(t, e, b, 1)
	mustCommit(t, e, c, 1)
	// b、c 均为就绪单例分量且 seq 相同，首成员按 (r,i) 比较：(0,2) 先于 (1,1)。
	checkExecute(t, e, []Instance{c, b, a})
	// 已执行后：相同仍是空操作，不同仍报冲突（seq 与依赖集合仍被记住）。
	if err := e.Commit(a, 3, []Instance{b, c}); err != nil {
		t.Fatalf("identical re-commit after execute: %v", err)
	}
	if err := e.Commit(a, 9, []Instance{b, c}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict after execute: got %v", err)
	}
}

// 构造参数校验。
func TestInvalidParams(t *testing.T) {
	for _, tc := range [][2]int{{0, 1}, {1, 0}, {-2, 5}, {3, -1}} {
		if _, err := NewExecutor(tc[0], tc[1]); !errors.Is(err, ErrInvalidParams) {
			t.Fatalf("NewExecutor(%d, %d): got %v", tc[0], tc[1], err)
		}
	}
}

// Commit 各类错误按固定优先级只报第一个，且被拒绝的操作不改变状态。
func TestCommitErrorsOrderedAndAtomic(t *testing.T) {
	e := mustNew(t, 2, 1)
	a, b := inst(0, 1), inst(1, 1)

	cases := []struct {
		name string
		in   Instance
		seq  int
		deps []Instance
		want error
	}{
		{"instance r out of range", inst(2, 1), 1, nil, ErrInvalidInstance},
		{"instance i < 1", inst(0, 0), 1, nil, ErrInvalidInstance},
		{"dep r out of range", a, 1, []Instance{inst(-1, 1)}, ErrInvalidInstance},
		{"dep i < 1", a, 1, []Instance{inst(1, 0)}, ErrInvalidInstance},
		{"instance bad beats seq=0", inst(9, 9), 0, nil, ErrInvalidInstance},
		{"seq zero", a, 0, nil, ErrInvalidSeq},
		{"seq=0 beats self dep", a, 0, []Instance{a}, ErrInvalidSeq},
		{"self dep", a, 1, []Instance{a}, ErrSelfDependency},
		{"self dep beats dup dep", a, 1, []Instance{a, a}, ErrSelfDependency},
		{"dup dep", a, 1, []Instance{b, b}, ErrDuplicateDep},
	}
	for _, tc := range cases {
		if err := e.Commit(tc.in, tc.seq, tc.deps); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
		t.Logf("拒绝 %-24s -> %v（状态不变）", tc.name, tc.want)
	}
	if got := e.Pending(); len(got) != 0 {
		t.Fatalf("rejected commits changed state: Pending() = %v", got)
	}

	// Cap=1：登记新实例时未执行已提交实例数已达 Cap。
	mustCommit(t, e, a, 1)
	if err := e.Commit(b, 1, nil); !errors.Is(err, ErrCapExceeded) {
		t.Fatalf("cap exceeded: got %v", err)
	}
	// 冲突检查优先于 Cap 检查。
	if err := e.Commit(a, 2, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict beats cap: got %v", err)
	}
	// 满员时相同重复登记仍是合法空操作。
	if err := e.Commit(a, 1, nil); err != nil {
		t.Fatalf("identical re-commit at full cap: %v", err)
	}
	if got, want := e.Pending(), []Instance{a}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Pending() = %v, want %v", got, want)
	}
	// 执行后腾出容量，b 可登记。
	checkExecute(t, e, []Instance{a})
	mustCommit(t, e, b, 1)
	checkExecute(t, e, []Instance{b})
}

// Pending 返回已提交未执行实例的 (r,i) 升序列表。
func TestPendingSorted(t *testing.T) {
	e := mustNew(t, 3, 8)
	mustCommit(t, e, inst(2, 1), 1)
	mustCommit(t, e, inst(0, 3), 1)
	mustCommit(t, e, inst(0, 1), 1)
	mustCommit(t, e, inst(1, 2), 1)
	want := []Instance{inst(0, 1), inst(0, 3), inst(1, 2), inst(2, 1)}
	if got := e.Pending(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Pending() = %v, want %v", got, want)
	}
}

// 同一已提交集合，无论 Commit 次序，一次 Execute 序列相同。
func TestCommitOrderDoesNotAffectExecute(t *testing.T) {
	type commit struct {
		in   Instance
		seq  int
		deps []Instance
	}
	base := []commit{
		{inst(0, 1), 4, []Instance{inst(1, 1)}},
		{inst(1, 1), 2, []Instance{inst(0, 1)}},
		{inst(0, 2), 1, []Instance{inst(1, 2)}},
		{inst(1, 2), 3, nil},
		{inst(0, 3), 5, []Instance{inst(0, 1), inst(0, 2)}},
	}
	orders := [][]int{
		{0, 1, 2, 3, 4},
		{4, 3, 2, 1, 0},
		{2, 0, 4, 1, 3},
	}
	var want []Instance
	for k, ord := range orders {
		e := mustNew(t, 2, 16)
		for _, idx := range ord {
			c := base[idx]
			mustCommit(t, e, c.in, c.seq, c.deps...)
		}
		got := e.Execute()
		t.Logf("提交次序 %v -> Execute() = %v", ord, got)
		if k == 0 {
			want = got
		} else if !reflect.DeepEqual(got, want) {
			t.Fatalf("order %v: Execute() = %v, want %v", ord, got, want)
		}
	}
}
