package merger

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func mustNew(t *testing.T, g, batch int64) *Merger {
	t.Helper()
	m, err := New(g, batch)
	if err != nil {
		t.Fatalf("New(%d, %d) = %v", g, batch, err)
	}
	return m
}

func mustAdd(t *testing.T, m *Merger, id string, p, s, n int64) {
	t.Helper()
	if err := m.Add(id, p, s, n); err != nil {
		t.Fatalf("Add(%q, %d, %d, %d) = %v", id, p, s, n, err)
	}
}

func expectNext(t *testing.T, m *Merger, want int64) {
	t.Helper()
	got, err := m.Next()
	if err != nil {
		t.Fatalf("Next() err = %v", err)
	}
	if got != want {
		t.Fatalf("Next() = %d, want %d", got, want)
	}
}

func expectWake(t *testing.T, m *Merger, w int64, fired []Fired, left int) WakeResult {
	t.Helper()
	res, err := m.Wake()
	if err != nil {
		t.Fatalf("Wake() err = %v", err)
	}
	if res.W != w || res.Left != left || !reflect.DeepEqual(res.Fired, fired) {
		t.Fatalf("Wake() = %+v, want W=%d Fired=%v Left=%d", res, w, fired, left)
	}
	return res
}

// checkInvariants 验证每次唤醒后的核心不变量：
// 唤醒间隔不小于 g、单次触发不超过 B、被触发者的新 n 严格大于本次 w。
func checkInvariants(t *testing.T, m *Merger, prevLast int64, hadLast bool, res WakeResult) {
	t.Helper()
	if hadLast && res.W < prevLast+m.g {
		t.Errorf("w=%d 与上次唤醒 %d 的间隔小于 g=%d", res.W, prevLast, m.g)
	}
	if len(res.Fired) > int(m.batch) {
		t.Errorf("单次唤醒触发 %d 个，超过 B=%d", len(res.Fired), m.batch)
	}
	if res.Left < 0 {
		t.Errorf("留下的候选个数为负: %d", res.Left)
	}
	for _, f := range res.Fired {
		tm := m.timers[f.ID]
		if tm == nil {
			t.Errorf("触发了不存在的定时器 %q", f.ID)
			continue
		}
		if tm.n <= res.W {
			t.Errorf("定时器 %q 触发后 n=%d 不大于本次 w=%d", f.ID, tm.n, res.W)
		}
		if f.Late < 0 || f.K < 0 {
			t.Errorf("定时器 %q 的 late=%d k=%d 出现负值", f.ID, f.Late, f.K)
		}
	}
}

func expectStats(t *testing.T, m *Merger, want Stats) {
	t.Helper()
	if got := m.Stats(); got != want {
		t.Fatalf("Stats() = %+v, want %+v", got, want)
	}
}

// 规格示例 1：g=4、B=2，a(P=10,s=2,n=0)、b(P=6,s=3,n=5)、c(P=5,s=0,n=9)。
func TestSpecExample1(t *testing.T) {
	m := mustNew(t, 4, 2)
	mustAdd(t, m, "a", 10, 2, 0)
	mustAdd(t, m, "b", 6, 3, 5)
	mustAdd(t, m, "c", 5, 0, 9)

	expectNext(t, m, 2)
	prevLast := int64(0)
	hadLast := false
	for _, want := range []struct {
		w     int64
		fired []Fired
		left  int
	}{
		{2, []Fired{{ID: "a", Late: 0, K: 0}}, 0},
		{8, []Fired{{ID: "b", Late: 0, K: 0}}, 0},
		{12, []Fired{{ID: "c", Late: 3, K: 0}, {ID: "a", Late: 0, K: 0}}, 1},
		{16, []Fired{{ID: "b", Late: 2, K: 0}, {ID: "c", Late: 2, K: 0}}, 0},
		{20, []Fired{{ID: "c", Late: 1, K: 0}, {ID: "b", Late: 0, K: 0}}, 1},
	} {
		res := expectWake(t, m, want.w, want.fired, want.left)
		checkInvariants(t, m, prevLast, hadLast, res)
		prevLast, hadLast = res.W, true
	}
	expectStats(t, m, Stats{Wakes: 5, Fired: 8, Late: 4, Skipped: 0})
	expectNext(t, m, 24) // e = min(22, 23, 24) = 22，last+g = 24
}

// 规格示例 2：单个 P=3、s=0、n=0，g=10，第二次唤醒 k=2 追赶。
func TestSpecExample2(t *testing.T) {
	m := mustNew(t, 10, 1)
	mustAdd(t, m, "x", 3, 0, 0)

	expectWake(t, m, 0, []Fired{{ID: "x", Late: 0, K: 0}}, 0)
	expectNext(t, m, 10) // max(e=3, last+g=10)
	res := expectWake(t, m, 10, []Fired{{ID: "x", Late: 7, K: 2}}, 0)
	checkInvariants(t, m, 0, true, res)
	if n := m.timers["x"].n; n != 12 {
		t.Fatalf("追赶后 n = %d, want 12", n)
	}
	expectStats(t, m, Stats{Wakes: 2, Fired: 2, Late: 1, Skipped: 2})
}

// n 恰等于 w 是候选，n = w+1 不是候选（即使其窗口尚未结束）。
func TestCandidateBoundaryNEqualsW(t *testing.T) {
	m := mustNew(t, 0, 8)
	mustAdd(t, m, "a", 10, 0, 5) // n+s = 5，决定 w = 5
	mustAdd(t, m, "b", 10, 3, 5) // n == w，是候选，尽管 n+s = 8 > w
	mustAdd(t, m, "c", 10, 3, 6) // n == w+1，不是候选

	res := expectWake(t, m, 5, []Fired{{ID: "a", Late: 0, K: 0}, {ID: "b", Late: 0, K: 0}}, 0)
	checkInvariants(t, m, 0, false, res)
	if n := m.timers["c"].n; n != 6 {
		t.Fatalf("非候选 c 的 n 被改动: %d", n)
	}
	// c 的 n+s = 9 成为新的 e。
	expectNext(t, m, 9)
	expectWake(t, m, 9, []Fired{{ID: "c", Late: 0, K: 0}}, 0)
}

// 窗口末端 n+s 恰等于 w 时延迟为 0。
func TestWindowEndEqualsWNoLate(t *testing.T) {
	m := mustNew(t, 0, 1)
	mustAdd(t, m, "a", 10, 2, 0)
	res := expectWake(t, m, 2, []Fired{{ID: "a", Late: 0, K: 0}}, 0)
	checkInvariants(t, m, 0, false, res)
	expectStats(t, m, Stats{Wakes: 1, Fired: 1, Late: 0, Skipped: 0})
}

// last+g 恰等于 e 时唤醒时刻取该值。
func TestLastPlusGEqualsE(t *testing.T) {
	m := mustNew(t, 4, 1)
	mustAdd(t, m, "a", 10, 2, 0)
	mustAdd(t, m, "b", 10, 0, 6)

	expectWake(t, m, 2, []Fired{{ID: "a", Late: 0, K: 0}}, 0)
	// e = min(12, 6) = 6，last+g = 2+4 = 6，二者相等。
	expectNext(t, m, 6)
	res := expectWake(t, m, 6, []Fired{{ID: "b", Late: 0, K: 0}}, 0)
	checkInvariants(t, m, 2, true, res)
}

// g=0 退化为逐窗口唤醒，同一时刻可连续唤醒多次。
func TestGZeroDegenerates(t *testing.T) {
	m := mustNew(t, 0, 1)
	mustAdd(t, m, "a", 5, 0, 0)
	mustAdd(t, m, "b", 5, 0, 0)

	expectWake(t, m, 0, []Fired{{ID: "a", Late: 0, K: 0}}, 1) // b 被批量上限留下
	expectWake(t, m, 0, []Fired{{ID: "b", Late: 0, K: 0}}, 0) // 同一时刻再次唤醒
	expectWake(t, m, 5, []Fired{{ID: "a", Late: 0, K: 0}}, 1)
	expectWake(t, m, 5, []Fired{{ID: "b", Late: 0, K: 0}}, 0)
	expectNext(t, m, 10)
	expectStats(t, m, Stats{Wakes: 4, Fired: 4, Late: 0, Skipped: 0})
}

// 批量上限造成连续延迟，随后按名义栅格追赶。
func TestBatchLimitDelayAndCatchup(t *testing.T) {
	m := mustNew(t, 5, 1)
	mustAdd(t, m, "a", 100, 0, 0)
	mustAdd(t, m, "b", 100, 0, 0)

	expectWake(t, m, 0, []Fired{{ID: "a", Late: 0, K: 0}}, 1)
	// b 被留下，e=0 但 last+g=5，w=5，b 延迟 5。
	expectWake(t, m, 5, []Fired{{ID: "b", Late: 5, K: 0}}, 0)
	expectWake(t, m, 100, []Fired{{ID: "a", Late: 0, K: 0}}, 1) // b 的 n=100 恰等于 w，被批量上限留下
	expectWake(t, m, 105, []Fired{{ID: "b", Late: 5, K: 0}}, 0)
	expectStats(t, m, Stats{Wakes: 4, Fired: 4, Late: 2, Skipped: 0})
}

// 跳过数 k 的向下取整：整除与不整除两种情形。
func TestSkipKFloor(t *testing.T) {
	// (w-n) 不整除 P：floor(7/3) = 2（规格示例 2 已覆盖），这里验证 floor(7/5) = 1。
	m := mustNew(t, 12, 1)
	mustAdd(t, m, "x", 5, 0, 0)
	expectWake(t, m, 0, []Fired{{ID: "x", Late: 0, K: 0}}, 0)
	expectWake(t, m, 12, []Fired{{ID: "x", Late: 7, K: 1}}, 0)
	if n := m.timers["x"].n; n != 15 {
		t.Fatalf("k=1 时 n = %d, want 15", n)
	}

	// (w-n) 整除 P：floor(5/5) = 1，n 增加 2P。
	m2 := mustNew(t, 10, 1)
	mustAdd(t, m2, "y", 5, 0, 0)
	expectWake(t, m2, 0, []Fired{{ID: "y", Late: 0, K: 0}}, 0)
	expectWake(t, m2, 10, []Fired{{ID: "y", Late: 5, K: 1}}, 0)
	if n := m2.timers["y"].n; n != 15 {
		t.Fatalf("整除时 n = %d, want 15", n)
	}
	expectStats(t, m2, Stats{Wakes: 2, Fired: 2, Late: 1, Skipped: 1})

	// w-n < P 时 k = 0。
	m3 := mustNew(t, 5, 1)
	mustAdd(t, m3, "z", 10, 0, 0)
	expectWake(t, m3, 0, []Fired{{ID: "z", Late: 0, K: 0}}, 0)
	expectWake(t, m3, 10, []Fired{{ID: "z", Late: 0, K: 0}}, 0)
	expectStats(t, m3, Stats{Wakes: 2, Fired: 2, Late: 0, Skipped: 0})
}

// 被留下的候选在下一次 e 已早于 last+g 时仍按 w = last+g 触发并计延迟。
func TestLeftoverWithNextEBeforeLastPlusG(t *testing.T) {
	m := mustNew(t, 10, 1)
	mustAdd(t, m, "a", 100, 0, 0)
	mustAdd(t, m, "b", 100, 5, 0)

	// w=0：候选 a(n+s=0)、b(n+s=5)，按 n+s 升序触发 a，b 留下。
	expectWake(t, m, 0, []Fired{{ID: "a", Late: 0, K: 0}}, 1)
	// e = min(100, 5) = 5 < last+g = 10，w = 10，b 延迟 10-5 = 5。
	res := expectWake(t, m, 10, []Fired{{ID: "b", Late: 5, K: 0}}, 0)
	checkInvariants(t, m, 0, true, res)
	// a、b 的 n 都是 100，恰等于 w=100 都是候选，b 被批量上限留下。
	expectWake(t, m, 100, []Fired{{ID: "a", Late: 0, K: 0}}, 1)
	expectWake(t, m, 110, []Fired{{ID: "b", Late: 5, K: 0}}, 0)
}

// Remove 立即退出 e 与候选的计算，e 随之重算；被留下的候选被 Remove 后不再触发。
func TestRemoveRecomputesE(t *testing.T) {
	m := mustNew(t, 0, 1)
	mustAdd(t, m, "a", 10, 0, 0)
	mustAdd(t, m, "b", 10, 0, 50)
	expectNext(t, m, 0)
	if err := m.Remove("a"); err != nil {
		t.Fatalf("Remove(a) = %v", err)
	}
	expectNext(t, m, 50) // e 从 0 重算为 50
	if err := m.Remove("b"); err != nil {
		t.Fatalf("Remove(b) = %v", err)
	}
	if _, err := m.Next(); !errors.Is(err, ErrNoTimers) {
		t.Fatalf("空集合 Next() err = %v, want ErrNoTimers", err)
	}

	// 被批量上限留下的候选被 Remove 后不再触发。
	mustAdd(t, m, "a", 10, 0, 0)
	mustAdd(t, m, "c", 10, 5, 0)
	expectWake(t, m, 0, []Fired{{ID: "a", Late: 0, K: 0}}, 1) // c 被留下
	if err := m.Remove("c"); err != nil {
		t.Fatalf("Remove(c) = %v", err)
	}
	expectNext(t, m, 10) // 只剩 a 的下一个窗口
	expectWake(t, m, 10, []Fired{{ID: "a", Late: 0, K: 0}}, 0)
	expectStats(t, m, Stats{Wakes: 2, Fired: 2, Late: 0, Skipped: 0})
}

// Add 的 n 恰等于当前时钟（上一次唤醒时刻）被接受，小 1 则被拒。
func TestAddNEqualsCurrentClock(t *testing.T) {
	m := mustNew(t, 0, 1)
	mustAdd(t, m, "a", 10, 0, 0)
	expectWake(t, m, 0, []Fired{{ID: "a", Late: 0, K: 0}}, 0) // last = 0

	if err := m.Add("b", 10, 0, 0); err != nil { // n == last，允许
		t.Fatalf("Add(n == last) = %v", err)
	}
	expectWake(t, m, 0, []Fired{{ID: "b", Late: 0, K: 0}}, 0)
	// a、b 的 n 都是 10，恰等于 w=10 都是候选，b 被批量上限留下。
	expectWake(t, m, 10, []Fired{{ID: "a", Late: 0, K: 0}}, 1) // last = 10

	if err := m.Add("c", 10, 0, 9); !errors.Is(err, ErrPastNominal) {
		t.Fatalf("Add(n = last-1) = %v, want ErrPastNominal", err)
	}
	if err := m.Add("c", 10, 0, 10); err != nil { // n == last，允许
		t.Fatalf("Add(n == last) = %v", err)
	}
	expectNext(t, m, 10)
}

// 构造参数的合法与非法边界。
func TestNewParamValidation(t *testing.T) {
	for _, g := range []int64{-1, -100, 1_000_000_001} {
		if _, err := New(g, 1); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("New(g=%d) = %v, want ErrInvalidParam", g, err)
		}
	}
	for _, b := range []int64{0, -1, 65, 100} {
		if _, err := New(0, b); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("New(B=%d) = %v, want ErrInvalidParam", b, err)
		}
	}
	if _, err := New(0, 1); err != nil {
		t.Errorf("New(0, 1) = %v", err)
	}
	if _, err := New(1_000_000_000, 64); err != nil {
		t.Errorf("New(1e9, 64) = %v", err)
	}
}

// Add/Remove 的拒绝原因可区分且按固定顺序只报第一个。
func TestAddRemoveErrorOrdering(t *testing.T) {
	m := mustNew(t, 0, 1)
	mustAdd(t, m, "a", 10, 0, 5)

	longID := strings.Repeat("x", 33)
	// 参数非法优先于编号重复。
	for _, tc := range []struct {
		id      string
		p, s, n int64
	}{
		{"", 10, 0, 5},                      // 空编号
		{longID, 10, 0, 5},                  // 编号超 32 字节
		{"x", 0, 0, 5},                      // P 越界
		{"x", 1_000_000_001, 0, 5},          // P 越界
		{"x", 10, -1, 5},                    // s 越界
		{"x", 10, 10, 5},                    // s 不小于 P
		{"x", 10, 0, -1},                    // n 越界
		{"x", 10, 0, 1_000_000_000_000_001}, // n 越界
		{"a", 0, 0, 0},                      // 编号重复但参数先非法
	} {
		if err := m.Add(tc.id, tc.p, tc.s, tc.n); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("Add(%q, %d, %d, %d) = %v, want ErrInvalidParam", tc.id, tc.p, tc.s, tc.n, err)
		}
	}
	// 编号重复优先于名义时刻过早：先让时钟前进到 5。
	expectWake(t, m, 5, []Fired{{ID: "a", Late: 0, K: 0}}, 0)
	if err := m.Add("a", 10, 0, 0); !errors.Is(err, ErrDuplicate) {
		t.Errorf("Add 重复编号 = %v, want ErrDuplicate", err)
	}
	if err := m.Add("b", 10, 0, 4); !errors.Is(err, ErrPastNominal) {
		t.Errorf("Add(n < last) = %v, want ErrPastNominal", err)
	}

	// 填满容量：名义时刻过早优先于容量已满。
	for i := 0; i < 63; i++ {
		mustAdd(t, m, fmt.Sprintf("t%02d", i), 10, 0, 5)
	}
	if err := m.Add("zz", 10, 0, 4); !errors.Is(err, ErrPastNominal) {
		t.Errorf("容量满且 n < last 时 = %v, want ErrPastNominal", err)
	}
	if err := m.Add("zz", 10, 0, 5); !errors.Is(err, ErrFull) {
		t.Errorf("容量满 = %v, want ErrFull", err)
	}
	if err := m.Add("a", 10, 0, 5); !errors.Is(err, ErrDuplicate) {
		t.Errorf("容量满且编号重复时 = %v, want ErrDuplicate", err)
	}

	// Remove：参数非法优先于不存在。
	if err := m.Remove(""); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Remove(空编号) = %v, want ErrInvalidParam", err)
	}
	if err := m.Remove(longID); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Remove(超长编号) = %v, want ErrInvalidParam", err)
	}
	if err := m.Remove("zz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove(不存在) = %v, want ErrNotFound", err)
	}
}

// 被拒绝的操作不得改变任何定时器、last 与计数。
func TestRejectedOpsKeepState(t *testing.T) {
	m := mustNew(t, 4, 2)
	mustAdd(t, m, "a", 10, 2, 0)
	mustAdd(t, m, "b", 6, 3, 5)
	expectWake(t, m, 2, []Fired{{ID: "a", Late: 0, K: 0}}, 0)
	before := m.Stats()
	nextBefore, _ := m.Next()

	rejected := []error{
		m.Add("", 10, 0, 0),   // 参数非法
		m.Add("a", 10, 0, 10), // 编号重复
		m.Add("c", 10, 0, 1),  // 名义时刻过早
		m.Remove(""),          // 参数非法
		m.Remove("zz"),        // 不存在
	}
	for i, err := range rejected {
		if err == nil {
			t.Fatalf("第 %d 个操作应被拒绝", i)
		}
	}
	if _, err := m.AdvanceTo(-1); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("AdvanceTo(-1) = %v, want ErrInvalidParam", err)
	}
	if _, err := m.AdvanceTo(1); !errors.Is(err, ErrClockBack) {
		t.Errorf("AdvanceTo(last-1) = %v, want ErrClockBack", err)
	}

	if got := m.Stats(); got != before {
		t.Errorf("被拒绝的操作改变了计数: %+v -> %+v", before, got)
	}
	if nextAfter, _ := m.Next(); nextAfter != nextBefore {
		t.Errorf("被拒绝的操作改变了 Next: %d -> %d", nextBefore, nextAfter)
	}
	if len(m.timers) != 2 {
		t.Errorf("被拒绝的操作改变了定时器集合: %d", len(m.timers))
	}
	// 后续正常操作不受影响。
	expectWake(t, m, 8, []Fired{{ID: "b", Late: 0, K: 0}}, 0)
}

// Next/Wake/AdvanceTo 在无定时器时报 ErrNoTimers；AdvanceTo 的拒绝顺序。
func TestEmptyAndAdvanceErrors(t *testing.T) {
	m := mustNew(t, 1, 1)
	if _, err := m.Next(); !errors.Is(err, ErrNoTimers) {
		t.Errorf("空集合 Next() = %v, want ErrNoTimers", err)
	}
	if _, err := m.Wake(); !errors.Is(err, ErrNoTimers) {
		t.Errorf("空集合 Wake() = %v, want ErrNoTimers", err)
	}
	if _, err := m.AdvanceTo(-1); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("空集合 AdvanceTo(-1) = %v, want ErrInvalidParam", err)
	}
	if _, err := m.AdvanceTo(1_000_000_000_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("AdvanceTo(t > 1e15) = %v, want ErrInvalidParam", err)
	}
	if _, err := m.AdvanceTo(0); !errors.Is(err, ErrNoTimers) {
		t.Errorf("空集合 AdvanceTo(0) = %v, want ErrNoTimers", err)
	}

	// 时钟回退优先于无定时器：先唤醒再删光。
	mustAdd(t, m, "a", 10, 0, 5)
	expectWake(t, m, 5, []Fired{{ID: "a", Late: 0, K: 0}}, 0)
	if err := m.Remove("a"); err != nil {
		t.Fatalf("Remove(a) = %v", err)
	}
	if _, err := m.AdvanceTo(4); !errors.Is(err, ErrClockBack) {
		t.Errorf("无定时器且 t < last 时 = %v, want ErrClockBack", err)
	}
	if _, err := m.AdvanceTo(5); !errors.Is(err, ErrNoTimers) {
		t.Errorf("无定时器且 t >= last 时 = %v, want ErrNoTimers", err)
	}
}

// AdvanceTo 在 Next() <= t 时连续唤醒，并把时钟停在最后一次唤醒时刻。
func TestAdvanceToBasic(t *testing.T) {
	m := mustNew(t, 4, 2)
	mustAdd(t, m, "a", 10, 2, 0)
	mustAdd(t, m, "b", 6, 3, 5)
	mustAdd(t, m, "c", 5, 0, 9)

	// t 之前没有唤醒：时钟不动。
	out, err := m.AdvanceTo(1)
	if err != nil || len(out) != 0 {
		t.Fatalf("AdvanceTo(1) = %v, %v", out, err)
	}
	expectStats(t, m, Stats{})
	expectNext(t, m, 2)

	// 与规格示例 1 相同的 5 次唤醒。
	out, err = m.AdvanceTo(20)
	if err != nil {
		t.Fatalf("AdvanceTo(20) err = %v", err)
	}
	if len(out) != 5 {
		t.Fatalf("AdvanceTo(20) 唤醒 %d 次, want 5", len(out))
	}
	wantW := []int64{2, 8, 12, 16, 20}
	for i, res := range out {
		if res.W != wantW[i] {
			t.Errorf("第 %d 次唤醒 w = %d, want %d", i, res.W, wantW[i])
		}
	}
	expectStats(t, m, Stats{Wakes: 5, Fired: 8, Late: 4, Skipped: 0})
	expectNext(t, m, 24)

	// 再次 AdvanceTo(20)：Next()=24 > 20，不再唤醒。
	out, err = m.AdvanceTo(20)
	if err != nil || len(out) != 0 {
		t.Fatalf("重复 AdvanceTo(20) = %v, %v", out, err)
	}
	expectStats(t, m, Stats{Wakes: 5, Fired: 8, Late: 4, Skipped: 0})
}

// AdvanceTo 单次调用至多执行 1e5 次 Wake，达上限即停且不报错。
func TestAdvanceToWakeCap(t *testing.T) {
	m := mustNew(t, 0, 1)
	mustAdd(t, m, "a", 1, 0, 0)
	out, err := m.AdvanceTo(1_000_000_000_000_000)
	if err != nil {
		t.Fatalf("AdvanceTo err = %v", err)
	}
	if len(out) != 100_000 {
		t.Fatalf("AdvanceTo 执行 %d 次 Wake, want 100000", len(out))
	}
	for i, res := range out {
		if res.W != int64(i) {
			t.Fatalf("第 %d 次唤醒 w = %d, want %d", i, res.W, i)
		}
	}
	expectStats(t, m, Stats{Wakes: 100_000, Fired: 100_000})
	expectNext(t, m, 100_000)
}

// Next() 不改变任何状态。
func TestNextDoesNotMutate(t *testing.T) {
	m := mustNew(t, 4, 2)
	mustAdd(t, m, "a", 10, 2, 0)
	for i := 0; i < 3; i++ {
		expectNext(t, m, 2)
	}
	expectStats(t, m, Stats{})
	expectWake(t, m, 2, []Fired{{ID: "a", Late: 0, K: 0}}, 0)
}

// Next() 的比较次数不随定时器总数线性增长：
// 以 n+s 为键的索引堆取栈顶为 O(1)，100 与 10000 两档下比较次数均为 0，
// 单次键值修复的比较次数为 O(log n)。
func TestNextComparisonsDoNotScaleLinearly(t *testing.T) {
	for _, size := range []int{100, 10000} {
		comparisons := 0
		h := newTimerHeap(func(a, b *timer) bool {
			comparisons++
			return lessEnd(a, b)
		})
		timers := make([]*timer, size)
		for i := range timers {
			timers[i] = &timer{id: fmt.Sprintf("t%d", i), p: 1_000_000_000, s: int64(i % 97), n: int64(i) * 7}
			h.push(timers[i])
		}
		comparisons = 0
		for i := 0; i < 1000; i++ {
			if h.top() == nil {
				t.Fatalf("size=%d 堆顶为空", size)
			}
		}
		if comparisons != 0 {
			t.Errorf("size=%d: 1000 次取顶产生 %d 次比较, want 0", size, comparisons)
		}
		comparisons = 0
		timers[0].n += 5
		h.fix(timers[0])
		if limit := 64; comparisons > limit {
			t.Errorf("size=%d: 单次键值修复 %d 次比较，超过对数上界 %d", size, comparisons, limit)
		}
		comparisons = 0
		h.remove(timers[size/2])
		if limit := 64; comparisons > limit {
			t.Errorf("size=%d: 单次删除 %d 次比较，超过对数上界 %d", size, comparisons, limit)
		}
	}
}

// Wake() 的候选考察次数不超过候选个数加一，不做全表扫描。
func TestWakeExaminationsBoundedByCandidates(t *testing.T) {
	m := mustNew(t, 0, 1)
	mustAdd(t, m, "a", 10, 0, 0)
	mustAdd(t, m, "b", 10, 0, 0)
	for i := 0; i < 62; i++ {
		mustAdd(t, m, fmt.Sprintf("x%02d", i), 10, 0, 1000)
	}
	res := expectWake(t, m, 0, []Fired{{ID: "a", Late: 0, K: 0}}, 1)
	// 候选为 a、b 共 2 个，考察次数 = 2 + 1 = 3，远小于总数 64。
	if want := int64(len(res.Fired) + res.Left + 1); m.wakeExam != want {
		t.Errorf("Wake 考察次数 = %d, want %d（候选数+1）", m.wakeExam, want)
	}
}

// 并发调用全部操作：结果等价于某个串行顺序（-race 下验证无数据竞争）。
func TestConcurrentCalls(t *testing.T) {
	m := mustNew(t, 1, 2)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("w%d", w)
			for i := 0; i < 300; i++ {
				switch i % 6 {
				case 0:
					m.Add(id, 10, 3, int64(i))
				case 1:
					m.Remove(id)
				case 2:
					m.Next()
				case 3:
					m.Wake()
				case 4:
					m.Stats()
				case 5:
					m.AdvanceTo(int64(i))
				}
			}
		}(w)
	}
	wg.Wait()
	st := m.Stats()
	if st.Wakes < 0 || st.Fired < 0 || st.Late < 0 || st.Skipped < 0 {
		t.Fatalf("并发后计数出现负值: %+v", st)
	}
}
