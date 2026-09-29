package rename

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

func testLogger(t *testing.T) *log.Logger {
	t.Helper()
	return log.New(testWriter{t}, "", 0)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func mustSnapshot(t *testing.T, e *Executor, want []string) {
	t.Helper()
	got := e.Snapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %v, want %v", got, want)
	}
	t.Logf("namespace=%v", got)
}

func TestSwap(t *testing.T) {
	e := NewExecutor(NewNamespace("a", "b"), testLogger(t))
	in := []Pair{{"a", "b"}, {"b", "a"}}
	t.Logf("input=%v", in)
	id, steps, err := e.Execute(in, "tmp")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []Step{{"a", "tmp0"}, {"b", "a"}, {"tmp0", "b"}}
	t.Logf("output steps=%v (判定依据: 互换构成 2-环, 从最小名 a 借临时名 tmp0 破环)", steps)
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	mustSnapshot(t, e, []string{"a", "b"})
	if id != 1 {
		t.Fatalf("batch id = %d, want 1", id)
	}
}

func TestLongChain(t *testing.T) {
	e := NewExecutor(NewNamespace("a", "b", "c", "d"), testLogger(t))
	in := []Pair{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d", "e"}}
	t.Logf("input=%v", in)
	_, steps, err := e.Execute(in, "tmp")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []Step{{"d", "e"}, {"c", "d"}, {"b", "c"}, {"a", "b"}}
	t.Logf("output steps=%v (判定依据: 单条链 a->b->c->d->e, 自空闲链尾 e 向链头倒序执行)", steps)
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	mustSnapshot(t, e, []string{"b", "c", "d", "e"})
}

func TestTenDisjointCycles(t *testing.T) {
	var names []string
	var in []Pair
	for i := 0; i < 10; i++ {
		x := fmt.Sprintf("x%02d", i)
		y := fmt.Sprintf("y%02d", i)
		names = append(names, x, y)
		in = append(in, Pair{x, y}, Pair{y, x})
	}
	sort.Strings(names)
	e := NewExecutor(NewNamespace(names...), testLogger(t))
	t.Logf("input=%v", in)
	_, steps, err := e.Execute(in, "tmp")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	t.Logf("output steps=%v", steps)

	// 统计临时名：恰用十个且互不相同。
	temps := map[string]int{}
	for _, s := range steps {
		if strings.HasPrefix(s.Old, "tmp") {
			temps[s.Old]++
		}
		if strings.HasPrefix(s.New, "tmp") {
			temps[s.New]++
		}
	}
	t.Logf("判定依据: 十个互不相交的环, 每环恰借一个临时名, temps=%v", temps)
	if len(temps) != 10 {
		t.Fatalf("distinct temp names = %d, want 10: %v", len(temps), temps)
	}
	for i := 0; i < 10; i++ {
		if _, ok := temps[fmt.Sprintf("tmp%d", i)]; !ok {
			t.Fatalf("missing temp name tmp%d in %v", i, temps)
		}
	}
	// 每步目标在执行时必须不存在：逐步重放验证。
	ns := NewNamespace(names...)
	for i, s := range steps {
		if !ns.Has(s.Old) || ns.Has(s.New) {
			t.Fatalf("step %d (%q -> %q) violates invariant", i, s.Old, s.New)
		}
		ns.rename(s.Old, s.New)
	}
	// 环按最小名升序处理：第一个环是 {x00, y00}，其最小名 x00 先改临时名。
	if steps[0] != (Step{"x00", "tmp0"}) {
		t.Fatalf("first step = %v, want {x00 tmp0}", steps[0])
	}
	mustSnapshot(t, e, names)
}

func TestTempNameSkipsOccupied(t *testing.T) {
	// tmp0 被命名空间占用，tmp1 是本批出现的名字，临时名应顺延到 tmp2。
	e := NewExecutor(NewNamespace("a", "b", "tmp0", "tmp1"), testLogger(t))
	in := []Pair{{"a", "b"}, {"b", "tmp1"}, {"tmp1", "a"}}
	t.Logf("input=%v (tmp0 已被占用, tmp1 是本批旧名)", in)
	_, steps, err := e.Execute(in, "tmp")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	t.Logf("output steps=%v (判定依据: 环 {a,b,tmp1} 最小名 a 先改临时名; tmp0 被占用、tmp1 属本批, 顺延到 tmp2)", steps)
	want := []Step{{"a", "tmp2"}, {"tmp1", "a"}, {"b", "tmp1"}, {"tmp2", "b"}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	mustSnapshot(t, e, []string{"a", "b", "tmp0", "tmp1"})
}

func TestFailAtStepK(t *testing.T) {
	for k := 0; k < 4; k++ {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			e := NewExecutor(NewNamespace("a", "b", "c", "d"), testLogger(t))
			before := e.Snapshot()
			in := []Pair{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d", "e"}}
			t.Logf("input=%v failAt=%d", in, k)
			_, _, err := e.execute(in, "tmp", k)
			if !errors.Is(err, ErrStepFailed) {
				t.Fatalf("err = %v, want ErrStepFailed", err)
			}
			t.Logf("output err=%v (判定依据: 第 %d 步注入失败, 已执行步骤逆序撤回)", err, k)
			mustSnapshot(t, e, before)
		})
	}
}

func TestUndoAndRepeatUndo(t *testing.T) {
	e := NewExecutor(NewNamespace("a", "b"), testLogger(t))
	before := e.Snapshot()
	in := []Pair{{"a", "b"}, {"b", "a"}}
	t.Logf("input=%v", in)
	id, _, err := e.Execute(in, "tmp")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := e.Undo(id); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	t.Logf("undo batch=%d ok (判定依据: 最近一次成功批次, 逆序反向执行)", id)
	mustSnapshot(t, e, before)

	if err := e.Undo(id); !errors.Is(err, ErrAlreadyUndone) {
		t.Fatalf("repeat undo err = %v, want ErrAlreadyUndone", err)
	}
	t.Logf("repeat undo rejected with ErrAlreadyUndone")
}

func TestUndoRejectedAfterNewerBatch(t *testing.T) {
	e := NewExecutor(NewNamespace("a", "b", "c", "d"), testLogger(t))
	id1, _, err := e.Execute([]Pair{{"a", "b"}, {"b", "a"}}, "tmp")
	if err != nil {
		t.Fatalf("Execute batch1: %v", err)
	}
	id2, _, err := e.Execute([]Pair{{"c", "d"}, {"d", "c"}}, "tmp")
	if err != nil {
		t.Fatalf("Execute batch2: %v", err)
	}
	if err := e.Undo(id1); !errors.Is(err, ErrStaleUndo) {
		t.Fatalf("undo stale batch err = %v, want ErrStaleUndo", err)
	}
	t.Logf("undo batch=%d rejected with ErrStaleUndo (判定依据: 其后已有批次 %d 生效)", id1, id2)
	if err := e.Undo(id2); err != nil {
		t.Fatalf("Undo latest: %v", err)
	}
	mustSnapshot(t, e, []string{"a", "b", "c", "d"})
}

func TestUndoWithoutBatch(t *testing.T) {
	e := NewExecutor(NewNamespace("a"), testLogger(t))
	if err := e.Undo(1); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("err = %v, want ErrNothingToUndo", err)
	}
}

func TestValidationRejections(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		in    []Pair
		want  error
	}{
		{"empty old", []string{"a"}, []Pair{{"", "b"}}, ErrEmptyName},
		{"empty new", []string{"a"}, []Pair{{"a", ""}}, ErrEmptyName},
		{"duplicate old", []string{"a", "b"}, []Pair{{"a", "c"}, {"a", "b"}}, ErrDuplicateOld},
		{"conflicting new", []string{"a", "b"}, []Pair{{"a", "c"}, {"b", "c"}}, ErrConflictingNew},
		{"old not found", []string{"a"}, []Pair{{"ghost", "b"}}, ErrOldNotFound},
		{"new exists", []string{"a", "b"}, []Pair{{"a", "b"}}, ErrNewNameExists},
		{"new exists via noop", []string{"a", "b"}, []Pair{{"a", "a"}, {"b", "a"}}, ErrNewNameExists},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewExecutor(NewNamespace(tc.names...), testLogger(t))
			before := e.Snapshot()
			t.Logf("input=%v", tc.in)
			_, _, err := e.Execute(tc.in, "tmp")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			t.Logf("output err=%v (判定依据: %v)", err, tc.want)
			mustSnapshot(t, e, before)
		})
	}
}

func TestSelfMapIsNoop(t *testing.T) {
	e := NewExecutor(NewNamespace("a", "b"), testLogger(t))
	in := []Pair{{"a", "a"}, {"b", "c"}}
	t.Logf("input=%v", in)
	_, steps, err := e.Execute(in, "tmp")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	t.Logf("output steps=%v (判定依据: a->a 为无操作被剔除, 仅剩 b->c)", steps)
	want := []Step{{"b", "c"}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
	mustSnapshot(t, e, []string{"a", "c"})
}

func TestOrderIndependent(t *testing.T) {
	perms := [][]Pair{
		{{"a", "b"}, {"b", "a"}, {"m", "n"}, {"n", "o"}},
		{{"n", "o"}, {"b", "a"}, {"m", "n"}, {"a", "b"}},
		{{"m", "n"}, {"a", "b"}, {"n", "o"}, {"b", "a"}},
	}
	var want []Step
	for i, in := range perms {
		e := NewExecutor(NewNamespace("a", "b", "m", "n"), testLogger(t))
		t.Logf("perm=%d input=%v", i, in)
		_, steps, err := e.Execute(in, "tmp")
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		t.Logf("output steps=%v", steps)
		if i == 0 {
			want = steps
		} else if !reflect.DeepEqual(steps, want) {
			t.Fatalf("perm %d steps = %v, want %v (顺序应无关)", i, steps, want)
		}
	}
	t.Logf("判定依据: 分量按最小名升序 (环{a,b} 在链 m->n->o 之前), 与给出顺序无关")
}

func TestConcurrentBatchesAndReaders(t *testing.T) {
	var names []string
	for i := 0; i < 8; i++ {
		names = append(names, fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i))
	}
	sort.Strings(names)
	e := NewExecutor(NewNamespace(names...), nil)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	// 并发读者：只能看到批次之前或之后的完整命名空间，绝不能看到临时名。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := e.Snapshot()
				if len(snap) != len(names) {
					t.Errorf("snapshot size = %d, want %d: %v", len(snap), len(names), snap)
					return
				}
				for _, n := range snap {
					if strings.HasPrefix(n, "tmp") {
						t.Errorf("reader observed temp name %q in %v", n, snap)
						return
					}
				}
			}
		}()
	}
	// 并发提交批次：彼此串行生效。
	var execWg sync.WaitGroup
	for i := 0; i < 8; i++ {
		execWg.Add(1)
		go func(i int) {
			defer execWg.Done()
			in := []Pair{
				{fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i)},
				{fmt.Sprintf("b%d", i), fmt.Sprintf("a%d", i)},
			}
			if _, _, err := e.Execute(in, "tmp"); err != nil {
				t.Errorf("Execute: %v", err)
			}
		}(i)
	}
	execWg.Wait()
	close(stop)
	wg.Wait()
	mustSnapshot(t, e, names)
	t.Logf("判定依据: 8 个互换批次并发提交串行生效, 读者快照始终无临时名且大小恒为 %d", len(names))
}
