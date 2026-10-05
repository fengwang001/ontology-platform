package cpm

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, n, e int, d int64) *CPM {
	t.Helper()
	c, err := New(n, e, d)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", n, e, d, err)
	}
	return c
}

func mustAddTask(t *testing.T, c *CPM, dur int64) int {
	t.Helper()
	id, _, err := c.AddTask(dur)
	if err != nil {
		t.Fatalf("AddTask(%d): %v", dur, err)
	}
	return id
}

func mustAddDep(t *testing.T, c *CPM, u, v int, lag int64) {
	t.Helper()
	if _, err := c.AddDep(u, v, lag); err != nil {
		t.Fatalf("AddDep(%d,%d,%d): %v", u, v, lag, err)
	}
}

func checkInts(t *testing.T, what string, got, want []int) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func checkI64(t *testing.T, what string, got, want int64) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %d, want %d", what, got, want)
	}
}

func checkReport(t *testing.T, got, want Report) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("report:\n got %+v\nwant %+v", got, want)
	}
}

func checkState(t *testing.T, c *CPM, es, ef, lf, tf, ff []int64) {
	t.Helper()
	for v := range es {
		if es != nil {
			checkI64(t, "ES", c.es[v], es[v])
		}
		if ef != nil {
			checkI64(t, "EF", c.ef[v], ef[v])
		}
		if lf != nil {
			checkI64(t, "LF", c.lf[v], lf[v])
		}
		if tf != nil {
			checkI64(t, "TF", c.tf[v], tf[v])
		}
		if ff != nil {
			got, err := c.FF(v)
			if err != nil {
				t.Fatalf("FF(%d): %v", v, err)
			}
			checkI64(t, "FF", got, ff[v])
		}
	}
}

// 题目给出的完整示例走查。
func TestWorkedExample(t *testing.T) {
	c := mustNew(t, 10, 20, 10)
	for _, d := range []int64{3, 2, 4, 1} {
		mustAddTask(t, c, d)
	}
	mustAddDep(t, c, 0, 1, 0)
	mustAddDep(t, c, 0, 2, 1)
	mustAddDep(t, c, 1, 3, 0)
	mustAddDep(t, c, 2, 3, -1)

	checkState(t, c,
		[]int64{0, 3, 4, 7},
		[]int64{3, 5, 8, 8},
		[]int64{5, 9, 10, 10},
		[]int64{2, 4, 2, 2},
		[]int64{0, 2, 0, 0})
	checkI64(t, "PF", c.PF(), 8)
	checkInts(t, "CriticalTasks", c.CriticalTasks(), []int{0, 2, 3})
	checkInts(t, "CriticalPath", c.CriticalPath(), []int{0, 2, 3})

	c.SetBaseline()

	rep, err := c.SetDuration(1, 6)
	if err != nil {
		t.Fatal(err)
	}
	checkReport(t, rep, Report{
		ChangedES:   []int{3},
		ChangedLF:   []int{0},
		OldPF:       8,
		NewPF:       10,
		CritAdded:   []int{1},
		CritRemoved: []int{2},
	})
	checkState(t, c,
		[]int64{0, 3, 4, 9},
		[]int64{3, 9, 8, 10},
		[]int64{3, 9, 10, 10},
		[]int64{0, 0, 2, 0},
		nil)
	checkInts(t, "CriticalTasks", c.CriticalTasks(), []int{0, 1, 3})
	checkInts(t, "CriticalPath", c.CriticalPath(), []int{0, 1, 3})

	v1, err := c.Variance(1)
	if err != nil {
		t.Fatal(err)
	}
	checkI64(t, "Variance(1)", v1, 4)
	v3, err := c.Variance(3)
	if err != nil {
		t.Fatal(err)
	}
	checkI64(t, "Variance(3)", v3, 2)

	rep2, err := c.SetConstraint(2, 6, -1)
	if err != nil {
		t.Fatal(err)
	}
	checkReport(t, rep2, Report{
		ChangedES: []int{2},
		OldPF:     10,
		NewPF:     10,
		CritAdded: []int{2},
	})
	// 依赖 (0,2) 不再是驱动边，关键路径不变。
	checkInts(t, "CriticalPath", c.CriticalPath(), []int{0, 1, 3})
	checkInts(t, "CriticalTasks", c.CriticalTasks(), []int{0, 1, 2, 3})
}

// 负搭接使后继 ES 小于前驱 EF。
func TestNegativeLag(t *testing.T) {
	c := mustNew(t, 10, 10, 100)
	mustAddTask(t, c, 5)
	mustAddTask(t, c, 2)
	mustAddDep(t, c, 0, 1, -3)
	checkState(t, c, []int64{0, 2}, []int64{5, 4}, nil, nil, nil)
	if c.es[1] >= c.ef[0] {
		t.Fatalf("expected ES(1)=%d < EF(0)=%d", c.es[1], c.ef[0])
	}
}

// snet 抬高 ES 后，原本驱动的依赖不再驱动。
func TestSNETRaisesESAndDepNoLongerDriving(t *testing.T) {
	c := mustNew(t, 10, 10, 100)
	mustAddTask(t, c, 3)
	mustAddTask(t, c, 2)
	mustAddDep(t, c, 0, 1, 0)
	// 当前 (0,1) 是驱动边：ES(1)=3=EF(0)。
	checkInts(t, "CriticalPath", c.CriticalPath(), []int{0, 1})
	rep, err := c.SetConstraint(1, 10, -1)
	if err != nil {
		t.Fatal(err)
	}
	checkInts(t, "ChangedES", rep.ChangedES, []int{1})
	checkI64(t, "ES(1)", c.es[1], 10)
	// ES(1)=10 != EF(0)+0=3，(0,1) 不再驱动。
	checkInts(t, "CriticalPath", c.CriticalPath(), []int{1})
}

// fnlt 压低 LF。
func TestFNLT(t *testing.T) {
	c := mustNew(t, 10, 10, 100)
	mustAddTask(t, c, 3)
	mustAddTask(t, c, 2)
	mustAddDep(t, c, 0, 1, 0)
	rep, err := c.SetConstraint(1, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	checkInts(t, "ChangedLF", rep.ChangedLF, []int{0, 1})
	checkI64(t, "LF(1)", c.lf[1], 4)
	checkI64(t, "LF(0)", c.lf[0], 2) // LS(1)=2 传播到前驱
	checkI64(t, "TF(1)", c.tf[1], -1)
}

// D 小于 PF 时 TF 全为负，关键任务仍是最小 TF 者。
func TestDeadlineTightAllNegativeTF(t *testing.T) {
	c := mustNew(t, 10, 10, 4)
	mustAddTask(t, c, 5)
	mustAddTask(t, c, 6)
	checkI64(t, "PF", c.PF(), 6)
	checkState(t, c, nil, nil, nil, []int64{-1, -2}, nil)
	checkInts(t, "CriticalTasks", c.CriticalTasks(), []int{1})
	checkInts(t, "CriticalPath", c.CriticalPath(), []int{1})
}

// D 宽松时关键任务的 TF 大于 0。
func TestDeadlineLoosePositiveTF(t *testing.T) {
	c := mustNew(t, 10, 10, 100)
	mustAddTask(t, c, 2)
	mustAddTask(t, c, 5)
	checkInts(t, "CriticalTasks", c.CriticalTasks(), []int{1})
	tf, err := c.TF(1)
	if err != nil {
		t.Fatal(err)
	}
	if tf <= 0 {
		t.Fatalf("expected positive TF for critical task, got %d", tf)
	}
}

// 最小 TF 变化使关键集合整体切换，而 ES 与 LF 均不变。
func TestCriticalSetSwitchWithoutESLFChange(t *testing.T) {
	c := mustNew(t, 10, 10, 10)
	mustAddTask(t, c, 5) // TF=5
	mustAddTask(t, c, 3) // TF=7
	checkInts(t, "CriticalTasks", c.CriticalTasks(), []int{0})
	rep, err := c.SetDuration(0, 1) // TF(0) 变为 9，最小 TF 变为 7
	if err != nil {
		t.Fatal(err)
	}
	checkReport(t, rep, Report{
		OldPF:       5,
		NewPF:       3,
		CritAdded:   []int{1},
		CritRemoved: []int{0},
	})
	checkInts(t, "CriticalTasks", c.CriticalTasks(), []int{1})
}

// 两条并列关键路径时 CriticalPath 取编号小者。
func TestParallelCriticalPaths(t *testing.T) {
	c := mustNew(t, 10, 10, 2)
	mustAddTask(t, c, 1)
	mustAddTask(t, c, 1)
	mustAddTask(t, c, 1)
	mustAddDep(t, c, 0, 2, 0)
	mustAddDep(t, c, 1, 2, 0)
	checkInts(t, "CriticalTasks", c.CriticalTasks(), []int{0, 1, 2})
	checkInts(t, "CriticalPath", c.CriticalPath(), []int{0, 2})

	// 分支处取编号最小的后继。
	c2 := mustNew(t, 10, 10, 2)
	mustAddTask(t, c2, 1)
	mustAddTask(t, c2, 1)
	mustAddTask(t, c2, 1)
	mustAddDep(t, c2, 0, 1, 0)
	mustAddDep(t, c2, 0, 2, 0)
	checkInts(t, "CriticalPath", c2.CriticalPath(), []int{0, 1})
}

// 自由时差：有后继取最小值，无后继取 PF-EF。
func TestFreeFloat(t *testing.T) {
	c := mustNew(t, 10, 10, 50)
	for range 4 {
		mustAddTask(t, c, 2)
	}
	mustAddDep(t, c, 0, 2, 0)  // ES(2)-0-EF(0)=2-2=0
	mustAddDep(t, c, 0, 3, 5)  // ES(3)-5-EF(0)=7-5-2=0
	mustAddDep(t, c, 1, 3, 10) // 抬高 ES(3)
	// ES(3)=max(0, EF(0)+5=7, EF(1)+10=12)=12
	checkI64(t, "ES(3)", c.es[3], 12)
	ff0, _ := c.FF(0) // min(2-0-2, 12-5-2)=0
	checkI64(t, "FF(0)", ff0, 0)
	ff1, _ := c.FF(1) // 12-10-2=0
	checkI64(t, "FF(1)", ff1, 0)
	ff2, _ := c.FF(2) // 无后继：PF-EF=14-4=10
	checkI64(t, "FF(2)", ff2, 10)
	ff3, _ := c.FF(3) // 无后继：PF-EF=14-14=0
	checkI64(t, "FF(3)", ff3, 0)
}

// 改工期使 LF 向前驱传播，直到被别的约束（fnlt）截断即停。
func TestDurationChangeLFPropagationCutoff(t *testing.T) {
	c := mustNew(t, 10, 10, 100)
	for range 3 {
		mustAddTask(t, c, 1)
	}
	mustAddDep(t, c, 0, 1, 0)
	mustAddDep(t, c, 1, 2, 0)
	if _, err := c.SetConstraint(1, 0, 50); err != nil {
		t.Fatal(err)
	}
	checkI64(t, "LF(1)", c.lf[1], 50)
	checkI64(t, "LF(0)", c.lf[0], 49)

	// LS(2)=60 > 50：LF(1) 被 fnlt=50 截断，不再向 LF(0) 传播。
	rep, err := c.SetDuration(2, 40)
	if err != nil {
		t.Fatal(err)
	}
	checkInts(t, "ChangedLF", rep.ChangedLF, nil)
	checkI64(t, "LF(1)", c.lf[1], 50)
	checkI64(t, "LF(0)", c.lf[0], 49)
	checkI64(t, "bwdEval", int64(c.bwdEval), 1)

	// LS(2)=40 < 50：LF(1) 变为 40 并继续传播到 LF(0)=39。
	rep2, err := c.SetDuration(2, 60)
	if err != nil {
		t.Fatal(err)
	}
	checkInts(t, "ChangedLF", rep2.ChangedLF, []int{0, 1})
	checkI64(t, "LF(1)", c.lf[1], 40)
	checkI64(t, "LF(0)", c.lf[0], 39)
}

// 删除依赖使后继 ES 回落。
func TestRemoveDepFallback(t *testing.T) {
	c := mustNew(t, 10, 10, 100)
	mustAddTask(t, c, 5)
	mustAddTask(t, c, 2)
	mustAddDep(t, c, 0, 1, 0)
	checkI64(t, "ES(1)", c.es[1], 5)
	rep, err := c.RemoveDep(0, 1)
	if err != nil {
		t.Fatal(err)
	}
	checkInts(t, "ChangedES", rep.ChangedES, []int{1})
	checkInts(t, "ChangedLF", rep.ChangedLF, []int{0})
	checkI64(t, "ES(1)", c.es[1], 0)
	checkI64(t, "LF(0)", c.lf[0], 100)
}

// 基线之后新建任务的 Variance 报告无基线。
func TestBaselineAndVariance(t *testing.T) {
	c := mustNew(t, 10, 10, 100)
	mustAddTask(t, c, 3)
	if _, err := c.Variance(0); !errors.Is(err, ErrNoBaseline) {
		t.Fatalf("want ErrNoBaseline, got %v", err)
	}
	c.SetBaseline()
	if _, err := c.SetDuration(0, 5); err != nil {
		t.Fatal(err)
	}
	v, err := c.Variance(0)
	if err != nil {
		t.Fatal(err)
	}
	checkI64(t, "Variance(0)", v, 2)
	mustAddTask(t, c, 1)
	if _, err := c.Variance(1); !errors.Is(err, ErrNoBaseline) {
		t.Fatalf("want ErrNoBaseline for task created after baseline, got %v", err)
	}
	if _, err := c.Variance(99); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("want ErrTaskNotFound, got %v", err)
	}
	// 覆盖旧基线。
	c.SetBaseline()
	v, err = c.Variance(0)
	if err != nil {
		t.Fatal(err)
	}
	checkI64(t, "Variance(0) after re-baseline", v, 0)
}

// 构造参数与各类拒绝原因可区分。
func TestErrorReasons(t *testing.T) {
	if _, err := New(0, 1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New N=0: %v", err)
	}
	if _, err := New(1, 0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New E=0: %v", err)
	}
	if _, err := New(1, 1, -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New D=-1: %v", err)
	}
	if _, err := New(100001, 1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New N too big: %v", err)
	}
	if _, err := New(1, 500001, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New E too big: %v", err)
	}
	if _, err := New(1, 1, 1000000000001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New D too big: %v", err)
	}

	c := mustNew(t, 2, 2, 10)
	if _, _, err := c.AddTask(-1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("AddTask(-1): %v", err)
	}
	if _, _, err := c.AddTask(1000001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("AddTask too big: %v", err)
	}
	mustAddTask(t, c, 1)
	mustAddTask(t, c, 1)
	if _, _, err := c.AddTask(1); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("AddTask over limit: %v", err)
	}
	if _, err := c.SetDuration(5, 1); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("SetDuration missing: %v", err)
	}
	if _, err := c.SetDuration(0, -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("SetDuration bad dur: %v", err)
	}
	if _, err := c.SetConstraint(0, -1, -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("SetConstraint bad snet: %v", err)
	}
	if _, err := c.SetConstraint(0, 0, -2); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("SetConstraint bad fnlt: %v", err)
	}
	if _, err := c.SetConstraint(9, 0, -1); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("SetConstraint missing: %v", err)
	}
}

// AddDep 按 参数非法、任务不存在、依赖已存在、依赖数已满、成环 的顺序只报第一个。
func TestAddDepErrorPrecedence(t *testing.T) {
	c := mustNew(t, 4, 1, 10)
	mustAddTask(t, c, 1)
	mustAddTask(t, c, 1)
	mustAddTask(t, c, 1)
	mustAddDep(t, c, 0, 1, 0)

	// 参数非法优先于任务不存在。
	if _, err := c.AddDep(0, 99, 1000001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("precedence invalid>missing: %v", err)
	}
	// 任务不存在优先于依赖已存在。
	if _, err := c.AddDep(0, 99, 0); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("precedence missing>exists: %v", err)
	}
	// 依赖已存在优先于依赖数已满（当前 ndeps=1=maxE）。
	if _, err := c.AddDep(0, 1, 0); !errors.Is(err, ErrDepExists) {
		t.Fatalf("precedence exists>full: %v", err)
	}
	// 依赖数已满优先于成环（u==v 也算成环）。
	if _, err := c.AddDep(2, 2, 0); !errors.Is(err, ErrDepLimit) {
		t.Fatalf("precedence full>cycle: %v", err)
	}
	// 成环：自环与间接环。
	c2 := mustNew(t, 4, 10, 10)
	mustAddTask(t, c2, 1)
	mustAddTask(t, c2, 1)
	mustAddTask(t, c2, 1)
	mustAddDep(t, c2, 0, 1, 0)
	mustAddDep(t, c2, 1, 2, 0)
	if _, err := c2.AddDep(0, 0, 0); !errors.Is(err, ErrCycle) {
		t.Fatalf("self loop: %v", err)
	}
	if _, err := c2.AddDep(2, 0, 0); !errors.Is(err, ErrCycle) {
		t.Fatalf("indirect cycle: %v", err)
	}
	// RemoveDep：任务不存在优先于依赖不存在。
	if _, err := c2.RemoveDep(0, 99); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("RemoveDep missing task: %v", err)
	}
	if _, err := c2.RemoveDep(2, 1); !errors.Is(err, ErrDepNotFound) {
		t.Fatalf("RemoveDep missing dep: %v", err)
	}
}

type snapshot struct {
	n, ndeps  int
	pf, minTF int64
	dur       []int64
	snet      []int64
	fnlt      []int64
	es        []int64
	ef        []int64
	lf        []int64
	tf        []int64
	succ      [][]edge
	pred      [][]edge
	baseline  []int64
	crit      []int
}

func takeSnapshot(c *CPM) snapshot {
	cp64 := func(s []int64) []int64 { return append([]int64(nil), s...) }
	cpE := func(s [][]edge) [][]edge {
		out := make([][]edge, len(s))
		for i := range s {
			out[i] = append([]edge(nil), s[i]...)
		}
		return out
	}
	return snapshot{
		n: c.n, ndeps: c.ndeps, pf: c.pf, minTF: c.minTF,
		dur: cp64(c.dur), snet: cp64(c.snet), fnlt: cp64(c.fnlt),
		es: cp64(c.es), ef: cp64(c.ef), lf: cp64(c.lf), tf: cp64(c.tf),
		succ: cpE(c.succ), pred: cpE(c.pred),
		baseline: cp64(c.baseline), crit: c.sortedCrit(),
	}
}

// 被拒绝的操作不得改变任何任务、依赖、基线与计数器。
func TestRejectedOpsKeepState(t *testing.T) {
	c := mustNew(t, 3, 3, 10)
	mustAddTask(t, c, 2)
	mustAddTask(t, c, 3)
	mustAddDep(t, c, 0, 1, 1)
	c.SetBaseline()

	rejections := []func() error{
		func() error { _, _, err := c.AddTask(-1); return err },
		func() error { _, _, err := c.AddTask(1000001); return err },
		func() error { _, err := c.AddDep(0, 1, 2000000); return err },
		func() error { _, err := c.AddDep(0, 9, 0); return err },
		func() error { _, err := c.AddDep(0, 1, 0); return err },
		func() error { _, err := c.AddDep(1, 1, 0); return err },
		func() error { _, err := c.AddDep(1, 0, 0); return err },
		func() error { _, err := c.RemoveDep(0, 9); return err },
		func() error { _, err := c.RemoveDep(1, 0); return err },
		func() error { _, err := c.SetDuration(9, 1); return err },
		func() error { _, err := c.SetDuration(0, -1); return err },
		func() error { _, err := c.SetConstraint(0, -1, -1); return err },
		func() error { _, err := c.SetConstraint(0, 0, 1000000000001); return err },
		func() error { _, err := c.SetConstraint(9, 0, -1); return err },
	}
	for i, op := range rejections {
		before := takeSnapshot(c)
		if err := op(); err == nil {
			t.Fatalf("rejection %d unexpectedly accepted", i)
		}
		after := takeSnapshot(c)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("rejection %d changed state:\nbefore %+v\nafter  %+v", i, before, after)
		}
	}
	// 任务数已满的拒绝也不改状态。
	mustAddTask(t, c, 1)
	before := takeSnapshot(c)
	if _, _, err := c.AddTask(1); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("want ErrTaskLimit")
	}
	if after := takeSnapshot(c); !reflect.DeepEqual(before, after) {
		t.Fatalf("task-limit rejection changed state")
	}
	// 依赖数已满的拒绝也不改状态。
	mustAddDep(t, c, 0, 2, 0)
	mustAddDep(t, c, 1, 2, 0)
	before = takeSnapshot(c)
	if _, err := c.AddDep(2, 0, 0); !errors.Is(err, ErrDepLimit) {
		t.Fatalf("want ErrDepLimit, got %v", err)
	}
	if after := takeSnapshot(c); !reflect.DeepEqual(before, after) {
		t.Fatalf("dep-limit rejection changed state")
	}
}
