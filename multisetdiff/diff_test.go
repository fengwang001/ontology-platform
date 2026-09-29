package multisetdiff

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
)

// testLogger 把每步输入、输出与判定依据打印到测试日志（go test -v 可见）。
type testLogger struct{ t *testing.T }

func (l testLogger) Logf(format string, args ...any) {
	l.t.Helper()
	l.t.Logf(format, args...)
}

func mustCommit(t *testing.T, v *View, changes []Change, want []Event) {
	t.Helper()
	got, err := v.Commit(changes, testLogger{t})
	if err != nil {
		t.Fatalf("Commit(%v) unexpected error: %v", changes, err)
	}
	if !eventsEqual(got, want) {
		t.Fatalf("Commit(%v) events = %v, want %v", changes, got, want)
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck after Commit: %v", err)
	}
}

func rejectCommit(t *testing.T, v *View, changes []Change, want error) {
	t.Helper()
	before := v.Snapshot()
	events, err := v.Commit(changes, testLogger{t})
	if !errors.Is(err, want) {
		t.Fatalf("Commit(%v) error = %v, want %v", changes, err, want)
	}
	if events != nil {
		t.Fatalf("rejected Commit produced events: %v", events)
	}
	after := v.Snapshot()
	if !mapsEqual(before, after) {
		t.Fatalf("rejected Commit changed view: before=%v after=%v", before, after)
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck after rejected Commit: %v", err)
	}
}

func eventsEqual(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mapsEqual(a, b map[string]int64) bool {
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

func chL(row string, count int64) Change {
	return Change{Side: Left, Delta: Delta{Row: row, Count: count}}
}

func chR(row string, count int64) Change {
	return Change{Side: Right, Delta: Delta{Row: row, Count: count}}
}

// TestRightSideFirst 验证右侧可以先于左侧到达：右侧单独出现时结果为空，
// 左侧随后到达时结果重数按 max(left-right,0) 恢复。
func TestRightSideFirst(t *testing.T) {
	v := New()

	mustCommit(t, v, []Change{chR("a", 3)}, nil)
	if got := v.Multiplicity("a"); got != 0 {
		t.Fatalf("right-only row multiplicity = %d, want 0", got)
	}

	mustCommit(t, v, []Change{chL("a", 2)}, nil) // 左 2 仍被右 3 完全抵消
	mustCommit(t, v, []Change{chL("a", 3)}, []Event{{Row: "a", Count: 2}})
	if got := v.Multiplicity("a"); got != 2 {
		t.Fatalf("a multiplicity = %d, want 2", got)
	}

	mustCommit(t, v, []Change{chR("a", -3)}, []Event{{Row: "a", Count: 3}})
	if got := v.Multiplicity("a"); got != 5 {
		t.Fatalf("a multiplicity after right removal = %d, want 5", got)
	}
	if !mapsEqual(v.Snapshot(), v.BatchRecompute()) {
		t.Fatalf("snapshot %v != batch %v", v.Snapshot(), v.BatchRecompute())
	}
}

// TestMinimalChanges 验证每条输入变更至多产生一条结果增量；
// 被 max(_,0) 截断（结果不变）时不产生任何事件。
func TestMinimalChanges(t *testing.T) {
	v := New()

	mustCommit(t, v, []Change{chL("x", 1)}, []Event{{"x", 1}})
	mustCommit(t, v, []Change{chR("x", 1)}, []Event{{"x", -1}}) // 差 1 -> 0
	mustCommit(t, v, []Change{chR("x", 5)}, nil)                // 结果恒为 0
	mustCommit(t, v, []Change{chL("x", 3)}, nil)                // 仍被右侧覆盖
	mustCommit(t, v, []Change{chL("x", 3)}, []Event{{"x", 1}})  // 跨过右侧 -> 一条 +1
	mustCommit(t, v, []Change{chR("x", -5)}, []Event{{"x", 5}}) // 右侧删空

	// 两侧都归零后键应从视图中消失
	mustCommit(t, v, []Change{chL("x", -7), chR("x", -1)}, []Event{{"x", -6}})
	if _, ok := v.Snapshot()["x"]; ok {
		t.Fatalf("zero-multiplicity row must not appear in view: %v", v.Snapshot())
	}
}

// TestUnderflow 验证任一侧删除导致负重数必须被拒绝。
func TestUnderflow(t *testing.T) {
	v := New()
	mustCommit(t, v, []Change{chL("a", 2)}, []Event{{"a", 2}})

	rejectCommit(t, v, []Change{chL("a", -3)}, ErrUnderflow)
	rejectCommit(t, v, []Change{chR("a", -1)}, ErrUnderflow) // 右侧当前为 0

	// 合法删除到恰好 0 是允许的
	mustCommit(t, v, []Change{chL("a", -2)}, []Event{{"a", -2}})

	// 串行内中间状态下溢同样整体拒绝
	mustCommit(t, v, []Change{chL("b", 1)}, []Event{{"b", 1}})
	rejectCommit(t, v, []Change{chL("b", 1), chL("b", -3)}, ErrUnderflow)
	if got := v.Multiplicity("b"); got != 1 {
		t.Fatalf("b = %d after rejected series, want 1", got)
	}
}

// TestIllegalInputs 验证各类非法输入给出互不相同的错误原因。
func TestIllegalInputs(t *testing.T) {
	v := New()
	mustCommit(t, v, []Change{chL("a", 1)}, []Event{{"a", 1}})

	reasons := []error{ErrEmptySeries, ErrZeroDelta, ErrUnknownSide, ErrUnderflow, ErrTooManyRows, ErrDeltaOverflow}
	for i := range reasons {
		for j := i + 1; j < len(reasons); j++ {
			if errors.Is(reasons[i], reasons[j]) {
				t.Fatalf("error reasons must be distinguishable: %v vs %v", reasons[i], reasons[j])
			}
		}
	}

	rejectCommit(t, v, nil, ErrEmptySeries)
	rejectCommit(t, v, []Change{}, ErrEmptySeries)
	rejectCommit(t, v, []Change{chL("a", 0)}, ErrZeroDelta)
	rejectCommit(t, v, []Change{{Side: Side(9), Delta: Delta{Row: "a", Count: 1}}}, ErrUnknownSide)

	// 整数溢出：左侧已达 MaxInt64 后再插入
	v2 := New()
	mustCommit(t, v2, []Change{chL("big", math.MaxInt64)}, []Event{{"big", math.MaxInt64}})
	rejectCommit(t, v2, []Change{chL("big", 1)}, ErrDeltaOverflow)

	if errors.Is(ErrDeltaOverflow, ErrUnderflow) {
		t.Fatalf("overflow and underflow must be distinguishable")
	}
}

// TestTooManyRows 验证不同行键数量超限被拒绝且不留痕。
func TestTooManyRows(t *testing.T) {
	v := NewWithLimit(2)
	mustCommit(t, v, []Change{chL("a", 1), chR("b", 1)}, []Event{{"a", 1}})

	before := v.Snapshot()
	rejectCommit(t, v, []Change{chL("c", 1)}, ErrTooManyRows)
	if !mapsEqual(v.Snapshot(), before) {
		t.Fatalf("state changed after too-many-rows rejection: %v", v.Snapshot())
	}

	// 删除不新增行；右侧 b 删空后腾出名额可再插入
	mustCommit(t, v, []Change{chR("b", -1)}, nil)
	mustCommit(t, v, []Change{chL("c", 1)}, []Event{{"c", 1}})
}

// TestRejectLeavesNoTrace 用混合合法/非法条目的串行验证原子性：
// 拒绝后两侧重数、视图与本次日志均无变化。
func TestRejectLeavesNoTrace(t *testing.T) {
	v := New()
	mustCommit(t, v, []Change{chL("a", 5), chR("a", 2)}, []Event{{"a", 5}, {"a", -2}})

	events, err := v.Commit([]Change{chL("a", 10), chR("missing", -1), chL("z", 1)}, testLogger{t})
	if !errors.Is(err, ErrUnderflow) {
		t.Fatalf("want ErrUnderflow, got %v", err)
	}
	if events != nil {
		t.Fatalf("rejected series must append no log events, got %v", events)
	}
	want := map[string]int64{"a": 3}
	if !mapsEqual(v.Snapshot(), want) {
		t.Fatalf("view = %v, want %v", v.Snapshot(), want)
	}
	if got := v.Multiplicity("z"); got != 0 {
		t.Fatalf("row z should not exist after rollback, multiplicity=%d", got)
	}

	// RejectError 必须定位到首个非法条目的下标（chR("missing", -1) 是第 2 条）。
	var rj *RejectError
	if !errors.As(err, &rj) || rj.Index != 1 {
		t.Fatalf("RejectError index = %v, want 1", err)
	}
}

// TestPrefixLogEqualsBatch 验证任意前缀的结果变更日志顺序应用后，
// 视图都等于从两侧当前重数做批量重算的结果（可复现性）。
func TestPrefixLogEqualsBatch(t *testing.T) {
	v := New()

	series := []Change{
		chL("a", 4), chR("a", 1),
		chR("b", 5),
		chL("b", 7),
		chL("c", 2), chR("c", 2),
		chR("a", 2),
		chL("a", -2),
		chL("b", -7),
		chR("b", -5),
		chL("a", 5),
	}

	replay := map[string]int64{}
	for i, ch := range series {
		events, err := v.Commit([]Change{ch}, testLogger{t})
		if err != nil {
			t.Fatalf("step %d unexpected error: %v", i, err)
		}
		for _, e := range events {
			replay[e.Row] += e.Count
			if replay[e.Row] == 0 {
				delete(replay, e.Row)
			}
		}
		// 每个前缀：日志重放视图 == 增量物化视图 == 批量重算
		if !mapsEqual(replay, v.Snapshot()) {
			t.Fatalf("prefix %d: replay %v != snapshot %v", i, replay, v.Snapshot())
		}
		if !mapsEqual(replay, v.BatchRecompute()) {
			t.Fatalf("prefix %d: replay %v != batch %v", i, replay, v.BatchRecompute())
		}
		for row, m := range replay {
			if m < 0 {
				t.Fatalf("prefix %d: negative multiplicity %q=%d", i, row, m)
			}
		}
		if err := v.SelfCheck(); err != nil {
			t.Fatalf("prefix %d: %v", i, err)
		}
	}
}

// TestConcurrent 验证多个执行体并发提交与并发自检/读视图是安全的，
// 且最终视图与批量重算一致（须以 -race 运行）。
func TestConcurrent(t *testing.T) {
	v := New()

	var writers sync.WaitGroup
	workers := 8
	ops := 200
	for w := 0; w < workers; w++ {
		writers.Add(1)
		go func(id int) {
			defer writers.Done()
			row := fmt.Sprintf("row-%d", id%4)
			for k := 0; k < ops; k++ {
				// 先插后删，成对出现；并发交错不应导致下溢。
				if _, err := v.Commit([]Change{chL(row, 1)}, nil); err != nil {
					t.Errorf("unexpected insert rejection: %v", err)
					return
				}
				if _, err := v.Commit([]Change{chL(row, -1)}, nil); err != nil {
					t.Errorf("unexpected delete rejection: %v", err)
					return
				}
			}
		}(w)
	}

	var readers sync.WaitGroup
	stop := make(chan struct{})
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if err := v.SelfCheck(); err != nil {
					t.Errorf("concurrent SelfCheck: %v", err)
					return
				}
				_ = v.Multiplicity("row-1")
				_ = v.Snapshot()
				_ = v.BatchRecompute()
			}
		}
	}()

	writers.Wait()
	close(stop)
	readers.Wait()

	// 每个 worker 插入/删除次数相同，最终所有行回到 0
	if len(v.Snapshot()) != 0 {
		t.Fatalf("final view = %v, want empty", v.Snapshot())
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("final SelfCheck: %v", err)
	}
}
