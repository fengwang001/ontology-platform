package pane

import (
	"math"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, s, d int64, p Policy, al, capacity int64) *Merger {
	t.Helper()
	m, err := New(s, d, p, al, capacity)
	if err != nil {
		t.Fatalf("New(%d,%d,%v,%d,%d) 意外拒绝: %v", s, d, p, al, capacity, err)
	}
	return m
}

func checkAdd(t *testing.T, m *Merger, ts, val int64, want []PaneOut) {
	t.Helper()
	got, err := m.Add(ts, val)
	if err != nil {
		t.Fatalf("Add(%d,%d) 意外拒绝: %v", ts, val, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Add(%d,%d) 迟到清单 = %+v, 期望 %+v", ts, val, got, want)
	}
}

func checkAdvance(t *testing.T, m *Merger, ip int64, want []PaneOut) {
	t.Helper()
	got, err := m.Advance(ip)
	if err != nil {
		t.Fatalf("Advance(%d) 意外拒绝: %v", ip, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Advance(%d) ON_TIME 清单 = %+v, 期望 %+v", ip, got, want)
	}
}

func checkState(t *testing.T, m *Merger, wantPanes []Pane, wantOut, wantDropped int64) {
	t.Helper()
	if got := m.Panes(); !reflect.DeepEqual(got, wantPanes) {
		t.Fatalf("Panes() = %+v, 期望 %+v", got, wantPanes)
	}
	if got := m.Output(); got != wantOut {
		t.Fatalf("Output() = %d, 期望 %d", got, wantOut)
	}
	if got := m.Dropped(); got != wantDropped {
		t.Fatalf("Dropped() = %d, 期望 %d", got, wantDropped)
	}
}

// TestSpecExample 逐步复现题目规格中的完整示例。
func TestSpecExample(t *testing.T) {
	m := mustNew(t, 10, 5, Earliest, 5, 3)

	checkAdd(t, m, 3, 1, nil)
	checkState(t, m, []Pane{
		{WS: -5, Count: 1, Sum: 1, MinTS: 3, MaxTS: 3, Hold: 3},
		{WS: 0, Count: 1, Sum: 1, MinTS: 3, MaxTS: 3, Hold: 3},
	}, -1, 0)

	checkAdvance(t, m, 4, nil)
	if got := m.Output(); got != 3 {
		t.Fatalf("Advance(4) 后 O = %d, 期望 3", got)
	}

	checkAdd(t, m, 2, 2, nil)
	checkState(t, m, []Pane{
		{WS: -5, Count: 2, Sum: 3, MinTS: 2, MaxTS: 3, Hold: 3},
		{WS: 0, Count: 2, Sum: 3, MinTS: 2, MaxTS: 3, Hold: 3},
	}, 3, 0)

	checkAdvance(t, m, 7, []PaneOut{{WS: -5, Count: 2, Sum: 3, TS: 3}})
	if got := m.Output(); got != 3 {
		t.Fatalf("Advance(7) 后 O = %d, 期望 3", got)
	}

	checkAdd(t, m, 6, 4, nil)
	checkState(t, m, []Pane{
		{WS: 0, Count: 3, Sum: 7, MinTS: 2, MaxTS: 6, Hold: 3},
		{WS: 5, Count: 1, Sum: 4, MinTS: 6, MaxTS: 6, Hold: 6},
	}, 3, 0)

	checkAdvance(t, m, 12, []PaneOut{{WS: 0, Count: 3, Sum: 7, TS: 3}})
	if got := m.Output(); got != 6 {
		t.Fatalf("Advance(12) 后 O = %d, 期望 6", got)
	}

	checkAdd(t, m, 9, 8, []PaneOut{{WS: 0, Count: 1, Sum: 8, TS: 9}})
	checkState(t, m, []Pane{
		{WS: 5, Count: 2, Sum: 12, MinTS: 6, MaxTS: 9, Hold: 6},
	}, 6, 0)

	checkAdvance(t, m, 15, []PaneOut{{WS: 5, Count: 2, Sum: 12, TS: 6}})
	if got := m.Output(); got != 15 {
		t.Fatalf("Advance(15) 后 O = %d, 期望 15", got)
	}

	checkAdd(t, m, 11, 16, []PaneOut{{WS: 5, Count: 1, Sum: 16, TS: 15}})
	checkState(t, m, []Pane{
		{WS: 10, Count: 1, Sum: 16, MinTS: 11, MaxTS: 11, Hold: 15},
	}, 15, 0)

	checkAdvance(t, m, 20, []PaneOut{{WS: 10, Count: 1, Sum: 16, TS: 15}})
	if got := m.Output(); got != 20 {
		t.Fatalf("Advance(20) 后 O = %d, 期望 20", got)
	}

	checkAdd(t, m, 1, 1, nil)
	checkState(t, m, []Pane{}, 20, 2)
}

// TestFloorCeilDiv 验证 floorDiv/ceilDiv 对负数按数学定义取整。
func TestFloorCeilDiv(t *testing.T) {
	for a := int64(-200); a <= 200; a++ {
		for b := int64(1); b <= 17; b++ {
			if got, want := floorDiv(a, b), int64(math.Floor(float64(a)/float64(b))); got != want {
				t.Fatalf("floorDiv(%d,%d) = %d, 期望 %d", a, b, got, want)
			}
			if got, want := ceilDiv(a, b), int64(math.Ceil(float64(a)/float64(b))); got != want {
				t.Fatalf("ceilDiv(%d,%d) = %d, 期望 %d", a, b, got, want)
			}
		}
	}
}

// TestNegativeWindowRounding 用暴力枚举窗口成员规则（ws <= ts < ws+S）
// 对照 Add 实际进入的窗格，覆盖 ts < S-D 时存在负 ws 窗口的情形；
// 向零截断会漏掉或多出窗口。
func TestNegativeWindowRounding(t *testing.T) {
	cases := []struct{ s, d int64 }{{10, 5}, {7, 3}, {16, 1}, {9, 9}}
	for _, c := range cases {
		for ts := int64(0); ts <= 3*c.s; ts++ {
			m := mustNew(t, c.s, c.d, Earliest, 0, MaxCap)
			checkAdd(t, m, ts, 1, nil)
			var want []Pane
			for k := -c.s/c.d - 2; k*c.d <= ts; k++ {
				ws := k * c.d
				if ws <= ts && ts < ws+c.s {
					want = append(want, Pane{WS: ws, Count: 1, Sum: 1, MinTS: ts, MaxTS: ts, Hold: ts})
				}
			}
			if len(want) == 0 {
				want = []Pane{}
			}
			if got := m.Panes(); !reflect.DeepEqual(got, want) {
				t.Fatalf("S=%d D=%d ts=%d: 窗格 = %+v, 期望 %+v", c.s, c.d, ts, got, want)
			}
		}
	}
}

// TestMixedClassification 同一个元素的三个窗口分别被丢弃、迟到发出与缓冲。
func TestMixedClassification(t *testing.T) {
	m := mustNew(t, 15, 5, Earliest, 5, 10)
	checkAdvance(t, m, 30, nil) // 空表，O = 30
	// ts=20 的窗口: ws=10(end=25, end+AL=30 <= I=30 丢弃),
	// ws=15(end=30 <= I < 35 迟到), ws=20(end=35 > I 缓冲)。
	checkAdd(t, m, 20, 7, []PaneOut{{WS: 15, Count: 1, Sum: 7, TS: 30}})
	checkState(t, m, []Pane{
		{WS: 20, Count: 1, Sum: 7, MinTS: 20, MaxTS: 20, Hold: 30},
	}, 30, 1)
}

// TestBoundaryEnd I 恰等于 end 为迟到，差 1 为缓冲。
func TestBoundaryEnd(t *testing.T) {
	// I = end-1 = 9：缓冲。
	m1 := mustNew(t, 10, 5, Earliest, 5, 10)
	checkAdvance(t, m1, 9, nil)
	checkAdd(t, m1, 5, 1, nil)
	checkState(t, m1, []Pane{
		{WS: 0, Count: 1, Sum: 1, MinTS: 5, MaxTS: 5, Hold: 9},
		{WS: 5, Count: 1, Sum: 1, MinTS: 5, MaxTS: 5, Hold: 9},
	}, 9, 0)

	// I = end = 10：ws=0 迟到，ws=5 仍缓冲。
	m2 := mustNew(t, 10, 5, Earliest, 5, 10)
	checkAdvance(t, m2, 10, nil)
	checkAdd(t, m2, 5, 1, []PaneOut{{WS: 0, Count: 1, Sum: 1, TS: 10}})
	checkState(t, m2, []Pane{
		{WS: 5, Count: 1, Sum: 1, MinTS: 5, MaxTS: 5, Hold: 10},
	}, 10, 0)
}

// TestBoundaryEndPlusAL I 恰等于 end+AL 为丢弃，差 1 为迟到。
func TestBoundaryEndPlusAL(t *testing.T) {
	// I = end+AL-1 = 14：ws=0 迟到。
	m1 := mustNew(t, 10, 5, Earliest, 5, 10)
	checkAdvance(t, m1, 14, nil)
	checkAdd(t, m1, 5, 1, []PaneOut{{WS: 0, Count: 1, Sum: 1, TS: 14}})
	if got := m1.Dropped(); got != 0 {
		t.Fatalf("Dropped() = %d, 期望 0", got)
	}

	// I = end+AL = 15：ws=0 丢弃，ws=5 迟到。
	m2 := mustNew(t, 10, 5, Earliest, 5, 10)
	checkAdvance(t, m2, 15, nil)
	checkAdd(t, m2, 5, 1, []PaneOut{{WS: 5, Count: 1, Sum: 1, TS: 15}})
	checkState(t, m2, []Pane{}, 15, 1)
}

// TestZeroAllowedLateness AL=0 时没有迟到窗格，end <= I 直接丢弃。
func TestZeroAllowedLateness(t *testing.T) {
	m := mustNew(t, 10, 5, Earliest, 0, 10)
	checkAdvance(t, m, 10, nil)
	// ws=0: I=10 >= end+0=10 丢弃；ws=5: 缓冲。
	checkAdd(t, m, 5, 1, nil)
	checkState(t, m, []Pane{
		{WS: 5, Count: 1, Sum: 1, MinTS: 5, MaxTS: 5, Hold: 10},
	}, 10, 1)

	checkAdvance(t, m, 15, []PaneOut{{WS: 5, Count: 1, Sum: 1, TS: 10}})
	// ws=0 与 ws=5 均丢弃，仍然没有任何迟到窗格。
	checkAdd(t, m, 6, 2, nil)
	checkState(t, m, []Pane{}, 15, 3)
}

// TestHoldClampEarliest EARLIEST 下较早 ts 的元素不使 hold 低于 O。
func TestHoldClampEarliest(t *testing.T) {
	m := mustNew(t, 10, 5, Earliest, 0, 10)
	checkAdd(t, m, 8, 1, nil) // ws=0,5，hold=8
	checkAdvance(t, m, 9, nil)
	if got := m.Output(); got != 8 {
		t.Fatalf("O = %d, 期望 8", got)
	}
	// ts=2 的元素进入 ws=0（ws=-5 因 AL=0 且 I=9 >= end=5 被丢弃），
	// raw=minTS=2 < O=8，hold 钳制到 8。
	checkAdd(t, m, 2, 2, nil)
	checkState(t, m, []Pane{
		{WS: 0, Count: 2, Sum: 3, MinTS: 2, MaxTS: 8, Hold: 8},
		{WS: 5, Count: 1, Sum: 1, MinTS: 8, MaxTS: 8, Hold: 8},
	}, 8, 1)
}

// TestEndPolicyHold END 策略 hold 恒为 end-1，不随元素变化。
func TestEndPolicyHold(t *testing.T) {
	m := mustNew(t, 10, 5, End, 0, 10)
	checkAdd(t, m, 3, 1, nil)
	checkAdd(t, m, 4, 2, nil)
	checkAdd(t, m, 0, 3, nil)
	checkState(t, m, []Pane{
		{WS: -5, Count: 3, Sum: 6, MinTS: 0, MaxTS: 4, Hold: 4},
		{WS: 0, Count: 3, Sum: 6, MinTS: 0, MaxTS: 4, Hold: 9},
	}, -1, 0)
	checkAdvance(t, m, 5, []PaneOut{{WS: -5, Count: 3, Sum: 6, TS: 4}})
	if got := m.Output(); got != 5 {
		t.Fatalf("O = %d, 期望 5", got)
	}
}

// TestLatestPolicyHoldRises LATEST 策略 hold 随更大 ts 上升，
// 后续 Advance 的 O 随之上升；I' 等于 I 时 O 仍可上升。
func TestLatestPolicyHoldRises(t *testing.T) {
	m := mustNew(t, 10, 5, Latest, 5, 10)
	checkAdd(t, m, 3, 1, nil) // ws=-5,0，hold=3
	checkAdvance(t, m, 4, nil)
	if got := m.Output(); got != 3 {
		t.Fatalf("O = %d, 期望 3", got)
	}
	checkAdd(t, m, 7, 2, nil) // ws=0 maxTS=7 -> hold=7；ws=5 新建 hold=7
	checkAdvance(t, m, 8, []PaneOut{{WS: -5, Count: 1, Sum: 1, TS: 3}})
	if got := m.Output(); got != 7 {
		t.Fatalf("O = %d, 期望 7", got)
	}
	checkAdd(t, m, 9, 3, nil) // ws=0,5 maxTS=9 -> hold=9
	// I' 等于 I=8：没有新到期窗格，但 O 由 7 上升到 min(8, 9)=8。
	checkAdvance(t, m, 8, nil)
	if got := m.Output(); got != 8 {
		t.Fatalf("I'=I 时 O = %d, 期望 8", got)
	}
	// 再次 Advance(8)：O 被 I' 钳住，保持 8。
	checkAdvance(t, m, 8, nil)
	if got := m.Output(); got != 8 {
		t.Fatalf("O = %d, 期望 8", got)
	}
}

// TestNoRemainingPanes 没有剩余窗格时 O 取 I'。
func TestNoRemainingPanes(t *testing.T) {
	m := mustNew(t, 10, 5, Earliest, 0, 10)
	checkAdd(t, m, 3, 1, nil)
	checkAdvance(t, m, 100, []PaneOut{
		{WS: -5, Count: 1, Sum: 1, TS: 3},
		{WS: 0, Count: 1, Sum: 1, TS: 3},
	})
	if got := m.Output(); got != 100 {
		t.Fatalf("O = %d, 期望 100", got)
	}
}

// TestLatePaneTimestampClamp 迟到窗格输出时间戳取 max(f, O)；
// END 策略下 f=end-1 可能小于 O，被钳制到 O。
func TestLatePaneTimestampClamp(t *testing.T) {
	m := mustNew(t, 10, 5, End, 100, 10)
	checkAdd(t, m, 0, 1, nil)
	checkAdvance(t, m, 100, []PaneOut{
		{WS: -5, Count: 1, Sum: 1, TS: 4},
		{WS: 0, Count: 1, Sum: 1, TS: 9},
	})
	if got := m.Output(); got != 100 {
		t.Fatalf("O = %d, 期望 100", got)
	}
	// ws=45(end=55) 与 ws=50(end=60) 均迟到，f=end-1=54,59 < O=100。
	checkAdd(t, m, 50, 5, []PaneOut{
		{WS: 45, Count: 1, Sum: 5, TS: 100},
		{WS: 50, Count: 1, Sum: 5, TS: 100},
	})
}

// TestCapacityRejection 容量不足时整个 Add 被拒绝：
// 迟到窗格不发出、丢弃不计数、状态不变。
func TestCapacityRejection(t *testing.T) {
	m := mustNew(t, 6, 1, Earliest, 1, 10)
	checkAdd(t, m, 10, 1, nil) // 窗格 ws=5..10，占 6 格
	checkAdvance(t, m, 12, []PaneOut{
		{WS: 5, Count: 1, Sum: 1, TS: 10},
		{WS: 6, Count: 1, Sum: 1, TS: 10},
	})
	checkAdd(t, m, 30, 2, nil) // 窗格 ws=25..30，表满 10 格

	before := m.Panes()
	// Add(11,3) 的窗口 ws=6..11：ws=6 为迟到（end=12 <= I=12 < 13=end+AL），
	// ws=7..10 已在表中，ws=11 需要新窗格 -> 10+1 > Cap=10，整体拒绝。
	late, err := m.Add(11, 3)
	if err != ErrCapacity {
		t.Fatalf("Add(11,3) err = %v, 期望 ErrCapacity", err)
	}
	if late != nil {
		t.Fatalf("被拒绝的 Add 返回迟到清单 %+v, 期望 nil", late)
	}
	checkState(t, m, before, 10, 0)
	if got := m.Input(); got != 12 {
		t.Fatalf("I = %d, 期望 12", got)
	}

	// Advance(13) 发出 ws=7 腾出一格后，同一 Add 生效：
	// ws=6 变为丢弃（I=13 >= end+AL=13），ws=7 变为迟到。
	checkAdvance(t, m, 13, []PaneOut{{WS: 7, Count: 1, Sum: 1, TS: 10}})
	checkAdd(t, m, 11, 3, []PaneOut{{WS: 7, Count: 1, Sum: 3, TS: 11}})
	if got := m.Dropped(); got != 1 {
		t.Fatalf("Dropped() = %d, 期望 1", got)
	}
}

// TestRejectedOpsKeepState 参数非法与水位回退的拒绝均可区分且不改状态。
func TestRejectedOpsKeepState(t *testing.T) {
	m := mustNew(t, 10, 5, Earliest, 5, 3)
	checkAdd(t, m, 3, 1, nil)
	checkAdvance(t, m, 4, nil)
	before := m.Panes()
	assertState := func(what string) {
		t.Helper()
		checkState(t, m, before, 3, 0)
		if got := m.Input(); got != 4 {
			t.Fatalf("%s 后 I = %d, 期望 4", what, got)
		}
	}

	for _, ts := range []int64{-1, MaxTS + 1} {
		if _, err := m.Add(ts, 0); err != ErrInvalidParam {
			t.Fatalf("Add(%d,0) err = %v, 期望 ErrInvalidParam", ts, err)
		}
	}
	for _, val := range []int64{-MaxVal - 1, MaxVal + 1} {
		if _, err := m.Add(0, val); err != ErrInvalidParam {
			t.Fatalf("Add(0,%d) err = %v, 期望 ErrInvalidParam", val, err)
		}
	}
	assertState("非法 Add")

	for _, ip := range []int64{-1, MaxTS + 1} {
		if _, err := m.Advance(ip); err != ErrInvalidParam {
			t.Fatalf("Advance(%d) err = %v, 期望 ErrInvalidParam", ip, err)
		}
	}
	if _, err := m.Advance(3); err != ErrRegression {
		t.Fatalf("Advance(3) err = %v, 期望 ErrRegression", err)
	}
	assertState("非法/回退 Advance")

	// 参数非法优先于容量不足：表已满（Cap=3，窗格 ws=-5,0 加 ws=5 共 3 格）。
	checkAdd(t, m, 7, 1, nil) // 新建 ws=5，表满
	if _, err := m.Add(MaxTS+1, 0); err != ErrInvalidParam {
		t.Fatalf("表满时 Add(MaxTS+1,0) err = %v, 期望 ErrInvalidParam", err)
	}
	// 容量不足与参数非法可区分。
	if _, err := m.Add(20, 0); err != ErrCapacity {
		t.Fatalf("表满时 Add(20,0) err = %v, 期望 ErrCapacity", err)
	}
}

// TestConstructorValidation 构造参数越界整体拒绝。
func TestConstructorValidation(t *testing.T) {
	good := []struct {
		s, d int64
		p    Policy
		al   int64
		cap  int64
	}{
		{1, 1, Earliest, 0, 1},
		{16, 1, Latest, MaxAL, MaxCap},
		{MaxS, MaxS, End, 5, 10},
	}
	for _, c := range good {
		if _, err := New(c.s, c.d, c.p, c.al, c.cap); err != nil {
			t.Fatalf("New(%+v) 意外拒绝: %v", c, err)
		}
	}
	bad := []struct {
		s, d int64
		p    Policy
		al   int64
		cap  int64
	}{
		{0, 1, Earliest, 0, 1},               // S < 1（D > S）
		{10, 0, Earliest, 0, 1},              // D < 1
		{10, 11, Earliest, 0, 1},             // D > S
		{17, 1, Earliest, 0, 1},              // S > 16*D
		{MaxS + 1, MaxS + 1, Earliest, 0, 1}, // S > 10^9
		{10, 5, Policy(3), 0, 1},             // 策略非法
		{10, 5, Policy(-1), 0, 1},            // 策略非法
		{10, 5, Earliest, -1, 1},             // AL < 0
		{10, 5, Earliest, MaxAL + 1, 1},      // AL > 10^9
		{10, 5, Earliest, 0, 0},              // Cap < 1
		{10, 5, Earliest, 0, MaxCap + 1},     // Cap > 10^6
	}
	for _, c := range bad {
		if _, err := New(c.s, c.d, c.p, c.al, c.cap); err != ErrInvalidParam {
			t.Fatalf("New(%+v) err = %v, 期望 ErrInvalidParam", c, err)
		}
	}
}

// TestHoldProbesTwoTier 窗格表分别有 10 个与 10000 个未到期窗格时，
// 同一个不发出窗格的 Advance 的 holdProbes 增量必须相等。
func TestHoldProbesTwoTier(t *testing.T) {
	deltas := make([]int64, 2)
	for i, n := range []int{10, 10000} {
		m := mustNew(t, 1, 1, Earliest, 0, MaxCap)
		for ts := int64(0); ts < int64(n); ts++ {
			if _, err := m.Add(ts, 1); err != nil {
				t.Fatalf("n=%d Add(%d,1) 意外拒绝: %v", n, ts, err)
			}
		}
		before := m.HoldProbes()
		checkAdvance(t, m, 0, nil) // 所有窗格 end >= 1，不发出任何窗格
		deltas[i] = m.HoldProbes() - before
	}
	if deltas[0] != deltas[1] {
		t.Fatalf("holdProbes 增量 10 窗格=%d, 10000 窗格=%d, 期望相等", deltas[0], deltas[1])
	}
}

// TestHoldProbesStale 失效项被惰性丢弃并计入 holdProbes，
// 且满足 增量 <= 本次发出窗格数 + 2 + 本次丢弃失效项数。
func TestHoldProbesStale(t *testing.T) {
	m := mustNew(t, 2, 1, Latest, 0, 10)
	checkAdd(t, m, 0, 1, nil) // ws=-1,0，hold=0
	checkAdd(t, m, 1, 2, nil) // ws=0 hold 升为 1（旧堆项失效），ws=1 hold=1

	before := m.HoldProbes()
	checkAdvance(t, m, 0, nil)
	if got := m.HoldProbes() - before; got != 1 {
		t.Fatalf("Advance(0) holdProbes 增量 = %d, 期望 1", got)
	}

	before = m.HoldProbes()
	checkAdvance(t, m, 1, []PaneOut{{WS: -1, Count: 1, Sum: 1, TS: 0}})
	// 堆项 (-1,0) 与 (0,0) 失效被丢弃，(0,1) 有效：共查看 3 项。
	if got := m.HoldProbes() - before; got != 3 {
		t.Fatalf("Advance(1) holdProbes 增量 = %d, 期望 3", got)
	}
	if got := m.Output(); got != 1 {
		t.Fatalf("O = %d, 期望 1", got)
	}
}

// TestReplayDeterminism 相同操作序列重放得到完全相同的清单、窗格表、O 与丢弃计数。
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]PaneOut, []PaneOut, []Pane, int64, int64) {
		m := mustNew(t, 10, 5, Latest, 5, 4)
		var lateAll, onTimeAll []PaneOut
		for _, a := range [][2]int64{{3, 1}, {2, 2}, {6, 4}, {9, 8}, {11, 16}, {1, 1}} {
			late, _ := m.Add(a[0], a[1])
			lateAll = append(lateAll, late...)
		}
		for _, ip := range []int64{4, 7, 12, 15, 20} {
			out, _ := m.Advance(ip)
			onTimeAll = append(onTimeAll, out...)
		}
		return lateAll, onTimeAll, m.Panes(), m.Output(), m.Dropped()
	}
	l1, o1, p1, out1, d1 := run()
	l2, o2, p2, out2, d2 := run()
	if !reflect.DeepEqual(l1, l2) || !reflect.DeepEqual(o1, o2) ||
		!reflect.DeepEqual(p1, p2) || out1 != out2 || d1 != d2 {
		t.Fatalf("重放结果不一致")
	}
}
