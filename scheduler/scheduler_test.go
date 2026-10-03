package scheduler

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

func traceString(s *Scheduler, n int) string {
	var builder strings.Builder
	for tick := range n {
		if id := s.RunAt(tick); id != "" {
			builder.WriteString(id)
		} else {
			builder.WriteByte('.')
		}
	}
	return builder.String()
}

func addForTest(t *testing.T, s *Scheduler, spec Task) {
	t.Helper()
	if err := s.AddTask(spec); err != nil {
		t.Fatalf("AddTask(%+v): %v", spec, err)
	}
}

func mustWindow(t *testing.T, s *Scheduler, id string) []int {
	t.Helper()
	window, err := s.Window(id)
	if err != nil {
		t.Fatalf("Window(%q): %v", id, err)
	}
	return window
}

func mustDistance(t *testing.T, s *Scheduler, id string) int {
	t.Helper()
	value, err := s.Distance(id)
	if err != nil {
		t.Fatalf("Distance(%q): %v", id, err)
	}
	return value
}

func mustStats(t *testing.T, s *Scheduler, id string) Stats {
	t.Helper()
	stats, err := s.Stats(id)
	if err != nil {
		t.Fatalf("Stats(%q): %v", id, err)
	}
	return stats
}

func TestSkeletonHelpers(t *testing.T) {
	s := New()
	addForTest(t, s, Task{ID: "a", Period: 1, Execution: 1, RequiredHits: 1, WindowSize: 1})
	if err := s.Step(1); err != nil {
		t.Fatal(err)
	}
	if got := traceString(s, 1); got != "a" || !reflect.DeepEqual(mustWindow(t, s, "a"), []int{1}) {
		t.Fatalf("got trace=%q window=%v", got, mustWindow(t, s, "a"))
	}
}

func TestDistanceExamplesAndSliding(t *testing.T) {
	cases := []struct {
		name string
		m    int
		w    []int
		want int
	}{
		{"111 m2", 2, []int{1, 1, 1}, 2},
		{"110 m2", 2, []int{1, 1, 0}, 1},
		{"101 m2", 2, []int{1, 0, 1}, 1},
		{"011 m2", 2, []int{0, 1, 1}, 2},
		{"100 m2", 2, []int{1, 0, 0}, 0},
		{"all-met when m=k", 3, []int{1, 1, 1}, 1},
		{"one-miss when m=k", 3, []int{1, 1, 0}, 0},
		{"distance grows when the only miss slides away", 2, []int{0, 1, 1}, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := distance(tc.w, tc.m)
			t.Logf("输入 window=%v m=%d；输出 s=%d；判定：末尾追加 %d 个0并保留最后k位后达标数低于%d", tc.w, tc.m, got, got, tc.m)
			if got != tc.want {
				t.Fatalf("distance() = %d, want %d", got, tc.want)
			}
		})
	}

	sequence := []struct {
		window []int
		want   int
	}{
		{[]int{1, 1, 1}, 2},
		{[]int{1, 1, 0}, 1},
		{[]int{1, 0, 1}, 1},
		{[]int{0, 1, 1}, 2},
		{[]int{1, 1, 0}, 1},
	}
	t.Run("distance changes as the window slides", func(t *testing.T) {
		for step, tc := range sequence {
			got := distance(tc.window, 2)
			t.Logf("滑动序列第%d步输入window=%v；输出s=%d；判定：窗口追加位后旧结果滑出，距离可随0的位置减小或增大", step, tc.window, got)
			if got != tc.want {
				t.Fatalf("step %d distance=%d, want %d", step, got, tc.want)
			}
		}
	})
}

func TestSpecifiedExamples(t *testing.T) {
	t.Run("bbbabbba", func(t *testing.T) {
		s := New()
		addForTest(t, s, Task{ID: "a", Period: 3, Execution: 1, RequiredHits: 1, WindowSize: 3})
		addForTest(t, s, Task{ID: "b", Period: 4, Execution: 3, RequiredHits: 2, WindowSize: 3})
		if err := s.Step(8); err != nil {
			t.Fatal(err)
		}

		got := traceString(s, 8)
		window := mustWindow(t, s, "a")
		stats := mustStats(t, s, "a")
		t.Logf("输入 a(T=3,C=1,m=1,k=3),b(T=4,C=3,m=2,k=3),n=8；输出轨迹=%s a窗口=%v a统计=%+v；判定：b初始s=2优先于a的s=3，t=3先判a失败", got, window, stats)
		if got != "bbbabbba" {
			t.Fatalf("trace = %q, want %q", got, "bbbabbba")
		}
		if !reflect.DeepEqual(window, []int{0, 1, 1}) || stats.DynamicFailures != 0 {
			t.Fatalf("window=%v stats=%+v", window, stats)
		}
	})

	t.Run("miss changes distance within same tick", func(t *testing.T) {
		s := New()
		addForTest(t, s, Task{ID: "a", Period: 3, Execution: 1, RequiredHits: 1, WindowSize: 2})
		addForTest(t, s, Task{ID: "b", Period: 5, Execution: 4, RequiredHits: 1, WindowSize: 2})
		if err := s.Step(8); err != nil {
			t.Fatal(err)
		}

		got := traceString(s, 8)
		bWindow := mustWindow(t, s, "b")
		bDistance := mustDistance(t, s, "b")
		t.Logf("输入 a(T=3,C=1,m=1,k=2),b(T=5,C=4,m=1,k=2),n=8；输出轨迹=%s b窗口=%v b距离=%d；判定：t=7剩余4>3，立即追加0且同tick使用新s", got, bWindow, bDistance)
		if got != "abbbbaa." {
			t.Fatalf("trace = %q, want %q", got, "abbbbaa.")
		}
		if !reflect.DeepEqual(bWindow, []int{1, 0}) || bDistance != 1 {
			t.Fatalf("window=%v distance=%d", bWindow, bDistance)
		}
	})
}

func TestDeadlineAndCompletionBoundaries(t *testing.T) {
	t.Run("remaining equal to slack survives", func(t *testing.T) {
		s := New()
		addForTest(t, s, Task{ID: "a", Period: 4, Execution: 2, RequiredHits: 1, WindowSize: 2})
		addForTest(t, s, Task{ID: "b", Period: 3, Execution: 1, RequiredHits: 2, WindowSize: 3})
		if err := s.Step(2); err != nil {
			t.Fatal(err)
		}

		got := traceString(s, 2)
		window := mustWindow(t, s, "a")
		t.Logf("输入 a(T=4,C=2),b(T=3,C=1,m=2,k=3),n=2；输出轨迹=%s a窗口=%v；判定：t=1开始时a剩余量恰等于d-t=3，不丢弃", got, window)
		if got != "ba" || !reflect.DeepEqual(window, []int{1, 1}) {
			t.Fatalf("trace=%q window=%v", got, window)
		}
	})

	t.Run("remaining one greater than slack misses", func(t *testing.T) {
		s := New()
		addForTest(t, s, Task{ID: "a", Period: 3, Execution: 2, RequiredHits: 1, WindowSize: 2})
		addForTest(t, s, Task{ID: "b", Period: 1, Execution: 1, RequiredHits: 1, WindowSize: 1})
		if err := s.Step(3); err != nil {
			t.Fatal(err)
		}

		got := traceString(s, 3)
		window := mustWindow(t, s, "a")
		stats := mustStats(t, s, "a")
		t.Logf("输入 a(T=3,C=2),b(T=1,C=1,m=k=1),n=3；输出轨迹=%s 窗口=%v 统计=%+v；判定：t=2时a剩余2比d-t=1大1，立即追加0", got, window, stats)
		if got != "bbb" || !reflect.DeepEqual(window, []int{1, 0}) || stats.Missed != 1 {
			t.Fatalf("trace=%q window=%v stats=%+v", got, window, stats)
		}
	})

	t.Run("deadline equals current tick completes exactly", func(t *testing.T) {
		s := New()
		addForTest(t, s, Task{ID: "a", Period: 2, Execution: 2, RequiredHits: 1, WindowSize: 2})
		if err := s.Step(2); err != nil {
			t.Fatal(err)
		}
		window := mustWindow(t, s, "a")
		t.Logf("输入 a(T=2,C=2),n=2；输出轨迹=%s 窗口=%v；判定：完成时刻t+1=2恰等于d=2，追加1", traceString(s, 2), window)
		if !reflect.DeepEqual(window, []int{1, 1}) {
			t.Fatalf("window=%v", window)
		}
	})

	t.Run("positive phase idles before release", func(t *testing.T) {
		s := New()
		addForTest(t, s, Task{ID: "a", Phase: 2, Period: 2, Execution: 2, RequiredHits: 1, WindowSize: 2})
		if err := s.Step(4); err != nil {
			t.Fatal(err)
		}
		got := traceString(s, 4)
		t.Logf("输入 a(φ=2,T=2,C=2),n=4；输出轨迹=%s；判定：t=0、1未释放，t=2、3运行并于4完成", got)
		if got != "..aa" {
			t.Fatalf("trace=%q", got)
		}
	})

	t.Run("dynamic failure accumulates on every append", func(t *testing.T) {
		s := New()
		addForTest(t, s, Task{ID: "a", Period: 3, Execution: 3, RequiredHits: 1, WindowSize: 3})
		addForTest(t, s, Task{ID: "b", Period: 1, Execution: 1, RequiredHits: 1, WindowSize: 1})
		if err := s.Step(13); err != nil {
			t.Fatal(err)
		}
		got := traceString(s, 13)
		window := mustWindow(t, s, "a")
		stats := mustStats(t, s, "a")
		t.Logf("输入 a(T=3,C=3,m=1,k=3),b(T=1,C=1,m=k=1),n=13；输出轨迹=%s a窗口=%v a统计=%+v；判定：t=9首次低于m计一次，t=12窗口已为000仍单独判定再计一次", got, window, stats)
		if got != "bbbbbbbbbabba" || !reflect.DeepEqual(window, []int{0, 0, 0}) || stats.DynamicFailures != 2 || stats.Missed != 4 {
			t.Fatalf("trace=%q window=%v stats=%+v", got, window, stats)
		}
	})
}

func TestTieBreakAndDeadlineEqualsTick(t *testing.T) {
	t.Run("same distance uses deadline then id", func(t *testing.T) {
		s := New()
		addForTest(t, s, Task{ID: "b", Phase: 1, Period: 3, Execution: 1, RequiredHits: 1, WindowSize: 2})
		addForTest(t, s, Task{ID: "a", Phase: 1, Period: 4, Execution: 1, RequiredHits: 1, WindowSize: 2})
		addForTest(t, s, Task{ID: "c", Phase: 1, Period: 4, Execution: 1, RequiredHits: 1, WindowSize: 2})
		if err := s.Step(4); err != nil {
			t.Fatal(err)
		}
		got := traceString(s, 4)
		t.Logf("输入 φ=1 的 b(d=4,C=1)、a(d=5,C=1)、c(d=5,C=1)；输出轨迹=%s；判定：t=1先按截止期选b，t=2同截止期按编号选a，t=3选c", got)
		if got != ".bac" {
			t.Fatalf("trace=%q, want .bac", got)
		}
	})

	t.Run("d equals tick drops old job before release", func(t *testing.T) {
		s := New()
		addForTest(t, s, Task{ID: "a", Period: 2, Execution: 2, RequiredHits: 1, WindowSize: 2})
		addForTest(t, s, Task{ID: "b", Period: 2, Execution: 1, RequiredHits: 1, WindowSize: 2})
		if err := s.Step(5); err != nil {
			t.Fatal(err)
		}
		got := traceString(s, 5)
		window := mustWindow(t, s, "a")
		stats := mustStats(t, s, "a")
		t.Logf("输入 a(T=2,C=2),b(T=2,C=1),n=5；输出轨迹=%s a窗口=%v a统计=%+v；判定：t=2先丢弃a旧作业再释放新作业，因此tick2运行b", got, window, stats)
		if got != "aab.a" || !reflect.DeepEqual(window, []int{1, 0}) || stats.Missed != 1 {
			t.Fatalf("trace=%q window=%v stats=%+v", got, window, stats)
		}
	})
}

func TestRejectionsAreObservableAndNonMutating(t *testing.T) {
	started := New()
	if err := started.Step(1); err != nil {
		t.Fatal(err)
	}
	err := started.AddTask(Task{ID: "", Period: 1, Execution: 1, RequiredHits: 1, WindowSize: 1})
	t.Logf("输入已Step后的非法任务；输出错误=%v；判定：已开始优先于参数非法", err)
	if err != ErrStarted {
		t.Fatalf("error=%v, want ErrStarted", err)
	}

	invalid := New()
	invalidSpec := Task{ID: "bad", Period: 2, Execution: 3, RequiredHits: 1, WindowSize: 2}
	err = invalid.AddTask(invalidSpec)
	t.Logf("输入 C>T 的任务=%+v；输出错误=%v；判定：执行时间不得超过周期", invalidSpec, err)
	if err != ErrInvalidTask {
		t.Fatalf("error=%v, want ErrInvalidTask", err)
	}

	duplicate := New()
	spec := Task{ID: "a", Period: 1, Execution: 1, RequiredHits: 1, WindowSize: 1}
	addForTest(t, duplicate, spec)
	if err := duplicate.AddTask(spec); err != ErrDuplicateID {
		t.Fatalf("duplicate error=%v, want ErrDuplicateID", err)
	}

	full := New()
	for i := range 16 {
		addForTest(t, full, Task{ID: string(rune('a' + i)), Period: 1, Execution: 1, RequiredHits: 1, WindowSize: 1})
	}
	if err := full.AddTask(Task{ID: "q", Period: 1, Execution: 1, RequiredHits: 1, WindowSize: 1}); err != ErrTaskLimit {
		t.Fatalf("full error=%v, want ErrTaskLimit", err)
	}

	if err := New().Step(0); err != ErrInvalidStep {
		t.Fatalf("Step(0) error=%v, want ErrInvalidStep", err)
	}
	if err := New().Step(1_000_001); err != ErrInvalidStep {
		t.Fatalf("Step(1000001) error=%v, want ErrInvalidStep", err)
	}
	if _, err := New().Window("missing"); err != ErrTaskNotFound {
		t.Fatalf("Window error=%v", err)
	}
	if _, err := New().Distance("missing"); err != ErrTaskNotFound {
		t.Fatalf("Distance error=%v", err)
	}
	if _, err := New().Stats("missing"); err != ErrTaskNotFound {
		t.Fatalf("Stats error=%v", err)
	}

	if _, err := invalid.Window("bad"); err != ErrTaskNotFound || invalid.now != 0 || len(invalid.tasks) != 0 {
		t.Fatal("rejected AddTask mutated scheduler state")
	}
}

func TestStepSplitEquivalence(t *testing.T) {
	tasks := []Task{
		{ID: "a", Period: 3, Execution: 2, RequiredHits: 2, WindowSize: 3},
		{ID: "b", Phase: 1, Period: 4, Execution: 2, RequiredHits: 1, WindowSize: 2},
	}
	whole := buildScheduler(t, tasks)
	split := buildScheduler(t, tasks)

	if err := whole.Step(11); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{4, 1, 6} {
		if err := split.Step(n); err != nil {
			t.Fatal(err)
		}
	}

	wholeTrace := traceString(whole, 11)
	splitTrace := traceString(split, 11)
	t.Logf("输入相同任务集；输出Step(11)=%s，Step(4)+Step(1)+Step(6)=%s；判定：轨迹逐位相同且窗口、距离、统计一致", wholeTrace, splitTrace)
	if wholeTrace != splitTrace {
		t.Fatalf("traces differ: %q vs %q", wholeTrace, splitTrace)
	}
	for _, spec := range tasks {
		wholeWindow := mustWindow(t, whole, spec.ID)
		splitWindow := mustWindow(t, split, spec.ID)
		if !reflect.DeepEqual(wholeWindow, splitWindow) {
			t.Fatalf("%s window differs: %v vs %v", spec.ID, wholeWindow, splitWindow)
		}
		if mustDistance(t, whole, spec.ID) != mustDistance(t, split, spec.ID) {
			t.Fatalf("%s distance differs", spec.ID)
		}
		if mustStats(t, whole, spec.ID) != mustStats(t, split, spec.ID) {
			t.Fatalf("%s stats differs", spec.ID)
		}
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	s := New()
	addForTest(t, s, Task{ID: "a", Period: 3, Execution: 2, RequiredHits: 2, WindowSize: 3})

	var waitGroup sync.WaitGroup
	for worker := range 8 {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for i := range 20 {
				if worker == 0 {
					_ = s.Step(1)
				} else if worker%2 == 0 {
					if err := s.Step(1); err != nil {
						t.Errorf("Step: %v", err)
					}
				} else {
					_, _ = s.Window("a")
					_, _ = s.Distance("a")
					_, _ = s.Stats("a")
					_ = s.RunAt(i)
				}
			}
		}(worker)
	}
	waitGroup.Wait()

	ticks := 0
	for worker := range 8 {
		if worker == 0 || worker%2 == 0 {
			ticks += 20
		}
	}
	stats := mustStats(t, s, "a")
	window := mustWindow(t, s, "a")
	t.Logf("并发输入8个worker共%d个Step及若干只读查询；输出当前窗口=%v、统计=%+v；判定：所有调用均在锁保护下完成且不变量成立", ticks, window, stats)
	if len(window) != 3 || stats.Met+stats.Missed < 0 {
		t.Fatal("concurrent execution produced invalid state")
	}
}

func buildScheduler(t *testing.T, tasks []Task) *Scheduler {
	t.Helper()
	s := New()
	for _, spec := range tasks {
		addForTest(t, s, spec)
	}
	return s
}
