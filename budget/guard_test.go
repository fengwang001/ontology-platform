package budget

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustGuard(t *testing.T, d int, p []int, fPct, dmin int, b int64) *Guard {
	t.Helper()
	g, err := NewGuard(d, p, fPct, dmin, b)
	if err != nil {
		t.Fatalf("NewGuard failed: %v", err)
	}
	return g
}

func spend(t *testing.T, g *Guard, day int, x int64) []Event {
	t.Helper()
	events, err := g.Spend(day, x)
	if err != nil {
		t.Fatalf("Spend(%d, %d) rejected: %v", day, x, err)
	}
	return events
}

func spendReject(t *testing.T, g *Guard, day int, x int64, want RejectReason) {
	t.Helper()
	events, err := g.Spend(day, x)
	if events != nil {
		t.Fatalf("Spend(%d, %d) returned events on rejection: %v", day, x, events)
	}
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("Spend(%d, %d) error = %v, want RejectError", day, x, err)
	}
	if rej.Reason != want {
		t.Fatalf("Spend(%d, %d) reason = %v, want %v", day, x, rej.Reason, want)
	}
}

func adjust(t *testing.T, g *Guard, b2 int64) []Event {
	t.Helper()
	events, err := g.AdjustBudget(b2)
	if err != nil {
		t.Fatalf("AdjustBudget(%d) rejected: %v", b2, err)
	}
	return events
}

func checkEvents(t *testing.T, got []Event, want []Event) {
	t.Helper()
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func ladder(pct int) Event { return Event{Kind: EventLadder, Pct: pct} }
func forecast() Event      { return Event{Kind: EventForecast} }
func froze() Event         { return Event{Kind: EventFroze} }
func thawed() Event        { return Event{Kind: EventThawed} }

func TestConfigValidation(t *testing.T) {
	valid := []struct {
		d          int
		p          []int
		fPct, dmin int
		b          int64
	}{
		{1, []int{1}, 1, 1, 1},
		{366, []int{1, 2, 3, 4, 5, 6, 7, 1000}, 1000, 366, MaxBudget},
		{30, []int{50, 80, 100}, 100, 3, 10000},
	}
	for _, c := range valid {
		if _, err := NewGuard(c.d, c.p, c.fPct, c.dmin, c.b); err != nil {
			t.Errorf("NewGuard(%+v) rejected valid config: %v", c, err)
		}
	}

	invalid := []struct {
		name       string
		d          int
		p          []int
		fPct, dmin int
		b          int64
	}{
		{"D too small", 0, []int{50}, 100, 1, 100},
		{"D too large", 367, []int{50}, 100, 1, 100},
		{"no ladders", 30, nil, 100, 1, 100},
		{"too many ladders", 30, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}, 100, 1, 100},
		{"pct zero", 30, []int{0}, 100, 1, 100},
		{"pct too large", 30, []int{1001}, 100, 1, 100},
		{"pct not increasing", 30, []int{80, 50}, 100, 1, 100},
		{"pct duplicated", 30, []int{50, 50}, 100, 1, 100},
		{"F zero", 30, []int{50}, 0, 1, 100},
		{"F too large", 30, []int{50}, 1001, 1, 100},
		{"Dmin zero", 30, []int{50}, 100, 0, 100},
		{"Dmin over D", 30, []int{50}, 100, 31, 100},
		{"B zero", 30, []int{50}, 100, 1, 0},
		{"B negative", 30, []int{50}, 100, 1, -5},
		{"B too large", 30, []int{50}, 100, 1, MaxBudget + 1},
	}
	for _, c := range invalid {
		g, err := NewGuard(c.d, c.p, c.fPct, c.dmin, c.b)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: err = %v, want ErrInvalidConfig", c.name, err)
		}
		if g != nil {
			t.Errorf("%s: guard = %v, want nil", c.name, g)
		}
	}
}

// TestSpecExample 完整走查题目给出的示例序列。
func TestSpecExample(t *testing.T) {
	g := mustGuard(t, 30, []int{50, 80, 100}, 100, 3, 10000)

	checkEvents(t, spend(t, g, 0, 3000), nil)
	checkEvents(t, spend(t, g, 1, 2500), []Event{ladder(50)})
	checkEvents(t, spend(t, g, 2, 500), []Event{forecast()})
	checkEvents(t, spend(t, g, 20, 4000), []Event{ladder(80), ladder(100), froze()})
	if !g.Frozen() {
		t.Fatal("expected frozen after S reaches B")
	}
	spendReject(t, g, 20, 1, RejectFrozen)
	checkEvents(t, spend(t, g, 21, -2500), []Event{thawed()})
	if g.Frozen() {
		t.Fatal("expected thawed after refund")
	}
	checkEvents(t, adjust(t, g, 20000), nil)
	st := g.Snapshot()
	if st.Forecast {
		t.Fatal("forecast flag should be cleared after budget raise")
	}
	for i, f := range st.Fired {
		if f {
			t.Fatalf("ladder %d should be rearmed after budget raise", i)
		}
	}
	checkEvents(t, spend(t, g, 22, 2600), []Event{ladder(50)})
}

func checkState(t *testing.T, g *Guard, want State) {
	t.Helper()
	got := g.Snapshot()
	if got.Spent != want.Spent || got.Cur != want.Cur || got.Budget != want.Budget ||
		got.Forecast != want.Forecast || got.Frozen != want.Frozen ||
		!reflect.DeepEqual(got.Fired, want.Fired) {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
	if got.Frozen != (got.Spent >= got.Budget) {
		t.Fatalf("frozen %v inconsistent with S=%d B=%d", got.Frozen, got.Spent, got.Budget)
	}
}

// 阶梯恰等触发、少 1 不触发。
func TestLadderExactBoundary(t *testing.T) {
	g := mustGuard(t, 30, []int{50}, 1000, 30, 10000)
	checkEvents(t, spend(t, g, 0, 4999), nil)
	checkEvents(t, spend(t, g, 1, 1), []Event{ladder(50)})
	// 已触发后再次满足不重复告警。
	checkEvents(t, spend(t, g, 2, 1), nil)
}

// 一次花费跨过多个阶梯，按 p 升序产生多个事件。
func TestLadderMultipleInOrder(t *testing.T) {
	g := mustGuard(t, 30, []int{10, 20, 30}, 1000, 30, 1000)
	checkEvents(t, spend(t, g, 0, 350), []Event{ladder(10), ladder(20), ladder(30)})
}

// 冻结时正数花费被拒绝，恰好越线的那一笔被接受。
func TestFreezeRejectsPositiveSpend(t *testing.T) {
	g := mustGuard(t, 30, []int{100}, 1000, 30, 1000)
	checkEvents(t, spend(t, g, 0, 999), nil)
	// 恰好越线的一笔被接受并冻结。
	checkEvents(t, spend(t, g, 1, 1), []Event{ladder(100), froze()})
	spendReject(t, g, 2, 1, RejectFrozen)
	spendReject(t, g, 2, 1000, RejectFrozen)
}

// 冻结时零或负数花费被接受。
func TestFrozenAcceptsZeroAndRefund(t *testing.T) {
	g := mustGuard(t, 30, []int{1000}, 1000, 30, 1000)
	checkEvents(t, spend(t, g, 0, 1000), []Event{froze()})
	// 零花费：冻结状态未变，无事件。
	checkEvents(t, spend(t, g, 1, 0), nil)
	if !g.Frozen() {
		t.Fatal("still expected frozen")
	}
	// 退款解冻。
	checkEvents(t, spend(t, g, 2, -1), []Event{thawed()})
}

// 退款不重新武装，再次越线不重复告警。
func TestRefundDoesNotRearm(t *testing.T) {
	g := mustGuard(t, 30, []int{50}, 1000, 30, 1000)
	checkEvents(t, spend(t, g, 0, 500), []Event{ladder(50)})
	checkEvents(t, spend(t, g, 1, -100), nil)
	checkEvents(t, spend(t, g, 2, 100), nil)
	checkState(t, g, State{Spent: 500, Cur: 2, Budget: 1000, Fired: []bool{true}})
}

// AdjustBudget 提高预算只清除不再成立的阶梯标志。
func TestAdjustRaiseRearmsSelectively(t *testing.T) {
	g := mustGuard(t, 30, []int{50, 80}, 1000, 30, 1000)
	checkEvents(t, spend(t, g, 0, 850), []Event{ladder(50), ladder(80)})
	// 85000 >= 1200*50=60000 保持；85000 < 1200*80=96000 清除。
	checkEvents(t, adjust(t, g, 1200), nil)
	checkState(t, g, State{Spent: 850, Cur: 0, Budget: 1200, Fired: []bool{true, false}})
	// 重新越线只补报被重新武装的 80。
	checkEvents(t, spend(t, g, 1, 110), []Event{ladder(80)})
}

// AdjustBudget 降低预算时当场产生新越线事件。
func TestAdjustLowerFiresImmediately(t *testing.T) {
	g := mustGuard(t, 30, []int{50, 80}, 100, 1, 1000)
	checkEvents(t, spend(t, g, 0, 600), []Event{ladder(50), forecast()})
	// 60000 >= 700*80=56000，当场补报 LADDER(80)；预测仍真不重复。
	checkEvents(t, adjust(t, g, 700), []Event{ladder(80)})
	checkState(t, g, State{Spent: 600, Cur: 0, Budget: 700,
		Fired: []bool{true, true}, Forecast: true})
}

// e 恰等于 Dmin 才评估预测，小一天不评估。
func TestForecastMinElapsedDays(t *testing.T) {
	g := mustGuard(t, 30, []int{1000}, 100, 3, 1000)
	// e=1、2 小于 Dmin=3，即使外推远超预算也不评估，f 保持假。
	checkEvents(t, spend(t, g, 0, 900), nil)
	checkEvents(t, spend(t, g, 1, 0), nil)
	if g.Snapshot().Forecast {
		t.Fatal("forecast flag must stay false before Dmin")
	}
	// e=3 恰等于 Dmin，proj=floor(900*30/3)=9000，900000 >= 100000 触发。
	checkEvents(t, spend(t, g, 2, 0), []Event{forecast()})
}

// proj 向下取整影响判定。
func TestForecastFloor(t *testing.T) {
	// S=23, D=7, e=2：真实值 80.5，向下取整 80。
	// 80*100=8000 < 50*161=8050，不触发（若不舍入则会触发）。
	g := mustGuard(t, 7, []int{1000}, 161, 2, 50)
	checkEvents(t, spend(t, g, 0, 10), nil)
	checkEvents(t, spend(t, g, 1, 13), nil)
	if g.Snapshot().Forecast {
		t.Fatal("floor(80.5)=80 must not satisfy proj*100 >= 8050")
	}
}

// 预测条件取等即触发，少 1 不触发。
func TestForecastExactBoundary(t *testing.T) {
	// proj*100 == B*F：100*100 == 200*50，恰等触发。
	g := mustGuard(t, 10, []int{1000}, 50, 1, 200)
	checkEvents(t, spend(t, g, 0, 10), []Event{forecast()})
	// 少 1：proj=90，9000 < 10000 不触发。
	g2 := mustGuard(t, 10, []int{1000}, 50, 1, 200)
	checkEvents(t, spend(t, g2, 0, 9), nil)
}

// proj*100 超出 int64 时用 128 位比较。
func TestForecast128Bit(t *testing.T) {
	g := mustGuard(t, 366, []int{1000}, 1000, 1, MaxBudget)
	var last []Event
	forecastCount := 0
	for i := 0; i < 1000; i++ {
		last = spend(t, g, 0, MaxSpendDelta)
		for _, ev := range last {
			if ev.Kind == EventForecast {
				forecastCount++
			}
		}
	}
	// S 到 10^15 时 proj*100≈3.66e19 超出 int64；若用 int64 比较会回绕
	// 误判为假，导致 f 被清除后又重复触发。128 位比较下 FORECAST 恰好一次。
	if forecastCount != 1 {
		t.Fatalf("forecast fired %d times, want exactly 1", forecastCount)
	}
	// 最后一笔：预测条件仍真（f 已为真，无新事件），仅越线冻结。
	checkEvents(t, last, []Event{froze()})
}

// 越线冻结 FROZE、退款解冻 THAWED，严格交替。
func TestFreezeThawAlternation(t *testing.T) {
	g := mustGuard(t, 30, []int{1000}, 1000, 30, 100)
	checkEvents(t, spend(t, g, 0, 100), []Event{froze()})
	checkEvents(t, spend(t, g, 1, -1), []Event{thawed()})
	checkEvents(t, spend(t, g, 2, 1), []Event{froze()})
	checkEvents(t, spend(t, g, 3, -100), []Event{thawed()})
}

// AdjustBudget 降到不大于 S 产生 FROZE（排在 LADDER 与 FORECAST 之后），
// 提高预算解冻产生 THAWED。
func TestAdjustFreezeAndThaw(t *testing.T) {
	g := mustGuard(t, 30, []int{50}, 100, 1, 10000)
	checkEvents(t, spend(t, g, 0, 100), nil)
	// B2=100：LADDER(50)（恰等）、FORECAST（proj=3000）、FROZE（S>=B2）。
	checkEvents(t, adjust(t, g, 100), []Event{ladder(50), forecast(), froze()})
	if !g.Frozen() {
		t.Fatal("expected frozen after lowering budget to S")
	}
	// B2=200：重新武装不触发（恰等保持），仅解冻。
	checkEvents(t, adjust(t, g, 200), []Event{thawed()})
	checkState(t, g, State{Spent: 100, Cur: 0, Budget: 200,
		Fired: []bool{true}, Forecast: true})
}

// 冻结状态未变时无 FROZE/THAWED 事件。
func TestNoFreezeEventsWhenUnchanged(t *testing.T) {
	g := mustGuard(t, 30, []int{1000}, 1000, 30, 1000)
	checkEvents(t, spend(t, g, 0, 100), nil)
	checkEvents(t, spend(t, g, 1, 200), nil)
	checkEvents(t, spend(t, g, 2, 700), []Event{froze()})
	checkEvents(t, spend(t, g, 3, 0), nil)
	checkEvents(t, spend(t, g, 4, -100), []Event{thawed()})
	checkEvents(t, spend(t, g, 5, 0), nil)
}

// f 为真后条件持续真不重复事件，变假后再变真产生新事件。
func TestForecastRearmAfterClear(t *testing.T) {
	g := mustGuard(t, 30, []int{1000}, 100, 1, 10000)
	checkEvents(t, spend(t, g, 0, 1000), []Event{forecast()})
	// 条件持续真，无新事件。
	checkEvents(t, spend(t, g, 1, 0), nil)
	// 退款使条件变假，f 清除，无事件。
	checkEvents(t, spend(t, g, 2, -1000), nil)
	if g.Snapshot().Forecast {
		t.Fatal("forecast flag should be cleared")
	}
	// 再次满足条件，重新产生 FORECAST。
	checkEvents(t, spend(t, g, 3, 2000), []Event{forecast()})
}

// 拒绝原因按顺序只报第一个，且被拒绝的操作不改变任何状态。
func TestRejectionPriorityAndStatePreserved(t *testing.T) {
	g := mustGuard(t, 30, []int{50}, 100, 1, 1000)
	checkEvents(t, spend(t, g, 0, 100), []Event{forecast()})
	checkEvents(t, spend(t, g, 5, 0), nil)
	before := g.Snapshot()

	// 参数非法优先于日期回退（day 越界且 day<cur）。
	spendReject(t, g, -1, 1, RejectInvalidParam)
	spendReject(t, g, 30, 1, RejectInvalidParam)
	spendReject(t, g, 5, MaxSpendDelta+1, RejectInvalidParam)
	spendReject(t, g, 5, -MaxSpendDelta-1, RejectInvalidParam)
	// 日期回退优先于冻结与额度越界。
	spendReject(t, g, 4, 1, RejectDayRegression)
	// 额度越界：S+x < 0。
	spendReject(t, g, 5, -200, RejectOutOfRange)
	checkState(t, g, State{
		Spent: before.Spent, Cur: before.Cur, Budget: before.Budget,
		Fired: before.Fired, Forecast: before.Forecast, Frozen: before.Frozen,
	})

	// 冻结后：正数花费报 FROZEN，日期回退仍优先报 DAY_REGRESSION。
	// Spend(5,0) 已把 f 置假，本次预测条件重新成立，FORECAST 再次触发。
	checkEvents(t, spend(t, g, 6, 900), []Event{ladder(50), forecast(), froze()})
	spendReject(t, g, 5, 1, RejectDayRegression)
	spendReject(t, g, 6, 1, RejectFrozen)
	checkState(t, g, State{Spent: 1000, Cur: 6, Budget: 1000,
		Fired: []bool{true}, Forecast: true, Frozen: true})

	// AdjustBudget 只可能因 B2 越界拒绝，且不改状态。
	if _, err := g.AdjustBudget(0); !errors.Is(err, &RejectError{Reason: RejectInvalidParam}) {
		var rej *RejectError
		if !errors.As(err, &rej) || rej.Reason != RejectInvalidParam {
			t.Fatalf("AdjustBudget(0) err = %v, want INVALID_PARAM", err)
		}
	}
	if _, err := g.AdjustBudget(MaxBudget + 1); err == nil {
		t.Fatal("AdjustBudget(MaxBudget+1) should be rejected")
	}
	checkState(t, g, State{Spent: 1000, Cur: 6, Budget: 1000,
		Fired: []bool{true}, Forecast: true, Frozen: true})
}

// 累计花费上界：恰好 10^15 被接受，超出被拒绝；冻结优先于额度越界。
func TestSpendUpperBound(t *testing.T) {
	g := mustGuard(t, 366, []int{1000}, 1000, 366, MaxBudget)
	for i := 0; i < 1000; i++ {
		spend(t, g, 0, MaxSpendDelta)
	}
	// e=1 小于 Dmin=366，预测不评估，f 保持假。
	checkState(t, g, State{Spent: MaxSpend, Cur: 0, Budget: MaxBudget,
		Fired: []bool{false}, Frozen: true})
	// 已冻结且 S+x 越界：冻结优先。
	spendReject(t, g, 0, 1, RejectFrozen)
	// 退款 1 解冻后，S+x=10^15+1 越界。
	checkEvents(t, spend(t, g, 0, -1), []Event{thawed()})
	spendReject(t, g, 0, 2, RejectOutOfRange)
	checkState(t, g, State{Spent: MaxSpend - 1, Cur: 0, Budget: MaxBudget,
		Fired: []bool{false}, Frozen: false})
	// 恰好回到 10^15 被接受。
	checkEvents(t, spend(t, g, 0, 1), []Event{froze()})
}

// 并发调用等价于某个串行顺序：最终状态确定且无数据竞争。
func TestConcurrentAccess(t *testing.T) {
	g := mustGuard(t, 1, []int{50}, 100, 1, MaxBudget)
	const workers = 8
	const perWorker = 100
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if _, err := g.Spend(0, 1); err != nil {
					t.Errorf("spend rejected: %v", err)
					return
				}
				_ = g.Frozen()
				_ = g.Snapshot()
			}
		}()
	}
	wg.Wait()
	// 800 远低于阶梯阈值 5*10^14，阶梯不触发；proj=800 不满足预测条件。
	checkState(t, g, State{Spent: workers * perWorker, Cur: 0, Budget: MaxBudget,
		Fired: []bool{false}, Forecast: false})
}
