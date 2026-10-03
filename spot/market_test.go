package spot

import (
	"math/big"
	"sync"
	"testing"
)

// wantEvent 为期望事件的简写。fee 仅对结束事件有意义。
type wantEvent struct {
	kind   EventKind
	id     int64
	reason EndReason
	start  int64
	end    int64
	fee    int64
}

func end(id int64, reason EndReason, start, end, fee int64) wantEvent {
	return wantEvent{kind: EventEnd, id: id, reason: reason, start: start, end: end, fee: fee}
}

func start(id int64) wantEvent {
	return wantEvent{kind: EventStart, id: id}
}

func checkEvents(t *testing.T, label string, got []Event, want []wantEvent) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: 事件数 got %d want %d: %+v", label, len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Kind != w.kind || g.ID != w.id {
			t.Fatalf("%s: 事件[%d] = {kind:%d id:%d}, want {kind:%d id:%d}",
				label, i, g.Kind, g.ID, w.kind, w.id)
		}
		if w.kind == EventStart {
			continue
		}
		if g.Reason != w.reason || g.Start != w.start || g.End != w.end {
			t.Fatalf("%s: 事件[%d] = {reason:%d seg:[%d,%d)}, want {reason:%d seg:[%d,%d)}",
				label, i, g.Reason, g.Start, g.End, w.reason, w.start, w.end)
		}
		if g.Fee == nil || g.Fee.Cmp(big.NewInt(w.fee)) != 0 {
			t.Fatalf("%s: 事件[%d] 费用 = %v, want %d", label, i, g.Fee, w.fee)
		}
		if g.Fee.Sign() < 0 {
			t.Fatalf("%s: 事件[%d] 费用为负: %v", label, i, g.Fee)
		}
	}
}

func mustMarket(t *testing.T, k int, pmin int64) *Market {
	t.Helper()
	m, err := NewMarket(k, pmin)
	if err != nil {
		t.Fatalf("NewMarket(%d, %d) 失败: %v", k, pmin, err)
	}
	return m
}

func mustRequest(t *testing.T, m *Market, id, bid, ts int64) []Event {
	t.Helper()
	ev, err := m.Request(id, bid, ts)
	if err != nil {
		t.Fatalf("Request(%d,%d,%d) 被拒绝: %v", id, bid, ts, err)
	}
	return ev
}

func mustTerminate(t *testing.T, m *Market, id, ts int64) []Event {
	t.Helper()
	ev, err := m.Terminate(id, ts)
	if err != nil {
		t.Fatalf("Terminate(%d,%d) 被拒绝: %v", id, ts, err)
	}
	return ev
}

func checkBill(t *testing.T, m *Market, id int64, want int64) {
	t.Helper()
	if got := m.Bill(id); got.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("Bill(%d) = %v, want %d", id, got, want)
	}
}

func checkPrice(t *testing.T, m *Market, x, want int64) {
	t.Helper()
	if got := m.PriceAt(x); got != want {
		t.Fatalf("price(%d) = %d, want %d", x, got, want)
	}
}

func checkRunning(t *testing.T, m *Market, want ...int64) {
	t.Helper()
	got := m.RunningIDs()
	if len(got) != len(want) {
		t.Fatalf("运行集合 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("运行集合 = %v, want %v", got, want)
		}
	}
}

// TestSpecExample 逐步走查题目示例：K=2、Pmin=10。
func TestSpecExample(t *testing.T) {
	m := mustMarket(t, 2, 10)

	// Request(a,50,0)：仅 1 个请求，价 10，a 开始运行。
	checkEvents(t, "Request(a,50,0)", mustRequest(t, m, 1, 50, 0), []wantEvent{start(1)})
	checkPrice(t, m, 0, 10)

	// Request(b,40,0)：两者运行，价 10。
	checkEvents(t, "Request(b,40,0)", mustRequest(t, m, 2, 40, 0), []wantEvent{start(2)})
	checkPrice(t, m, 0, 10)

	// Request(c,45,1800)：b 被挤出，段 [0,1800) 中断，floor=0 小时，费用 0；
	// c 开始运行；出清价为第 3 名 b 的 40。
	checkEvents(t, "Request(c,45,1800)", mustRequest(t, m, 3, 45, 1800), []wantEvent{
		end(2, ReasonMarketInterrupt, 0, 1800, 0),
		start(3),
	})
	checkPrice(t, m, 1799, 10)
	checkPrice(t, m, 1800, 40)

	// Terminate(a,3700)：a 的段 [0,3700) 用户终止，ceil=2 小时，
	// 价 price(0)=10、price(3600)=40，费用 50；b 重新运行；价回到 10。
	checkEvents(t, "Terminate(a,3700)", mustTerminate(t, m, 1, 3700), []wantEvent{
		end(1, ReasonUserTerminate, 0, 3700, 50),
		start(2),
	})
	checkPrice(t, m, 3699, 40)
	checkPrice(t, m, 3700, 10)

	// Terminate(c,5400)：c 的段 [1800,5400) 用户终止，ceil(3600/3600)=1 小时，
	// 费用 price(1800)=40，恰在整点边界终止不收下一小时。
	checkEvents(t, "Terminate(c,5400)", mustTerminate(t, m, 3, 5400), []wantEvent{
		end(3, ReasonUserTerminate, 1800, 5400, 40),
	})

	// Request(d,30,6000)：仅 b、d 两个请求，价 Pmin 不变。
	checkEvents(t, "Request(d,30,6000)", mustRequest(t, m, 4, 30, 6000), []wantEvent{start(4)})
	checkPrice(t, m, 6000, 10)

	// SetCapacity(1,7200)：d 的段 [6000,7200) 中断，floor=0 小时费用 0；
	// 出清价为第 2 名 d 的 30。
	ev, err := m.SetCapacity(1, 7200)
	if err != nil {
		t.Fatalf("SetCapacity(1,7200) 被拒绝: %v", err)
	}
	checkEvents(t, "SetCapacity(1,7200)", ev, []wantEvent{
		end(4, ReasonMarketInterrupt, 6000, 7200, 0),
	})
	checkPrice(t, m, 7199, 10)
	checkPrice(t, m, 7200, 30)

	checkBill(t, m, 1, 50)
	checkBill(t, m, 2, 0)
	checkBill(t, m, 3, 40)
	checkBill(t, m, 4, 0)
	checkRunning(t, m, 2)
}

// TestClearingPriceAtKAndKPlus1 恰有 K 个请求时价为 Pmin，K+1 个时为第 K+1 名的 bid。
func TestClearingPriceAtKAndKPlus1(t *testing.T) {
	m := mustMarket(t, 2, 10)
	mustRequest(t, m, 1, 50, 0)
	checkPrice(t, m, 0, 10) // 1 个请求，不足 K+1
	mustRequest(t, m, 2, 40, 0)
	checkPrice(t, m, 0, 10) // 恰 K 个，仍为 Pmin
	mustRequest(t, m, 3, 45, 0)
	checkPrice(t, m, 0, 40) // K+1 个，价为第 K+1 名（排名 50,45,40）的 bid
	mustRequest(t, m, 4, 47, 0)
	checkPrice(t, m, 0, 45) // 排名 50,47,45,40，第 3 名为 45
}

// TestTieBreakBySubmission bid 并列时先提交者胜出。
func TestTieBreakBySubmission(t *testing.T) {
	m := mustMarket(t, 1, 10)
	checkEvents(t, "Request(1,20,0)", mustRequest(t, m, 1, 20, 0), []wantEvent{start(1)})
	// 同价后提交，只能等待，不产生事件。
	checkEvents(t, "Request(2,20,0)", mustRequest(t, m, 2, 20, 0), nil)
	checkRunning(t, m, 1)
	// 先提交者终止后，后提交者补上。
	checkEvents(t, "Terminate(1,100)", mustTerminate(t, m, 1, 100), []wantEvent{
		end(1, ReasonUserTerminate, 0, 100, 20),
		start(2),
	})
	checkRunning(t, m, 2)
}

// TestRunningBidEqualsClearingPrice 运行请求 bid 恰等于出清价仍运行。
func TestRunningBidEqualsClearingPrice(t *testing.T) {
	m := mustMarket(t, 1, 10)
	mustRequest(t, m, 1, 20, 0)
	mustRequest(t, m, 2, 20, 0)
	// 出清价为第 2 名的 20，恰等于运行者 1 的 bid，1 仍运行。
	checkPrice(t, m, 0, 20)
	checkRunning(t, m, 1)
}

// TestNewRequestEvictsLowest 新请求挤出最低名次者，被挤出者进入等待。
func TestNewRequestEvictsLowest(t *testing.T) {
	m := mustMarket(t, 2, 10)
	mustRequest(t, m, 1, 50, 0)
	mustRequest(t, m, 2, 40, 0)
	checkEvents(t, "Request(3,45,100)", mustRequest(t, m, 3, 45, 100), []wantEvent{
		end(2, ReasonMarketInterrupt, 0, 100, 0),
		start(3),
	})
	checkRunning(t, m, 1, 3)
	// 被挤出者仍在请求集合中：终止等待中的 2 无费用。
	checkEvents(t, "Terminate(2,200)", mustTerminate(t, m, 2, 200), nil)
	checkBill(t, m, 2, 0)
}

// TestInterruptFreeHours 被中断的段不足一小时免费，满一小时按整数小时收取，
// 中断时刻恰在整点边界不收新一小时。
func TestInterruptFreeHours(t *testing.T) {
	m := mustMarket(t, 1, 10)
	mustRequest(t, m, 1, 50, 0)
	// 1800 秒时被挤出：floor(1800/3600)=0 小时，免费。
	checkEvents(t, "Request(2,60,1800)", mustRequest(t, m, 2, 60, 1800), []wantEvent{
		end(1, ReasonMarketInterrupt, 0, 1800, 0),
		start(2),
	})
	checkBill(t, m, 1, 0)
	// 5400 秒时 2 被挤出：段 [1800,5400) 恰 3600 秒，floor=1 小时，
	// 恰在整点边界不收新一小时，费用 price(1800)=50。
	checkEvents(t, "Request(3,70,5400)", mustRequest(t, m, 3, 70, 5400), []wantEvent{
		end(2, ReasonMarketInterrupt, 1800, 5400, 50),
		start(3),
	})
	checkBill(t, m, 2, 50)
	// 9001 秒时 3 被挤出：段 [5400,9001) 为 3601 秒，floor=1 小时。
	checkEvents(t, "Request(4,80,9001)", mustRequest(t, m, 4, 80, 9001), []wantEvent{
		end(3, ReasonMarketInterrupt, 5400, 9001, 60),
		start(4),
	})
	checkBill(t, m, 3, 60)
}

// TestUserTerminateCeil 用户终止按向上取整，恰在整点边界不多收。
func TestUserTerminateCeil(t *testing.T) {
	m := mustMarket(t, 2, 10)
	mustRequest(t, m, 1, 50, 0)
	mustRequest(t, m, 2, 40, 0)
	// 1 秒即终止：ceil(1/3600)=1 小时。
	checkEvents(t, "Terminate(1,1)", mustTerminate(t, m, 1, 1), []wantEvent{
		end(1, ReasonUserTerminate, 0, 1, 10),
	})
	// 恰 3600 秒终止：ceil=1 小时，不多收。
	mustRequest(t, m, 3, 50, 1)
	checkEvents(t, "Terminate(3,3601)", mustTerminate(t, m, 3, 3601), []wantEvent{
		end(3, ReasonUserTerminate, 1, 3601, 10),
	})
	// 3602 秒：ceil(3602/3600)=2 小时。
	checkEvents(t, "Terminate(2,3602)", mustTerminate(t, m, 2, 3602), []wantEvent{
		end(2, ReasonUserTerminate, 0, 3602, 20),
	})
	checkBill(t, m, 1, 10)
	checkBill(t, m, 3, 10)
	checkBill(t, m, 2, 20)
}

// TestHourPriceAtHourStart 第 k 小时的价取该小时起点时刻的价；
// 同一 t 多次改价以最后一次为准。
func TestHourPriceAtHourStart(t *testing.T) {
	m := mustMarket(t, 1, 10)
	mustRequest(t, m, 1, 50, 0)
	// t=100：出清价变为 40，历史 [(0,10),(100,40)]。
	mustRequest(t, m, 2, 40, 100)
	checkPrice(t, m, 100, 40)
	// 同一 t=100 再改价：终止 1 后出清价变为 30，覆盖 (100,40)。
	mustRequest(t, m, 3, 30, 100)
	checkEvents(t, "Terminate(1,100)", mustTerminate(t, m, 1, 100), []wantEvent{
		end(1, ReasonUserTerminate, 0, 100, 10),
		start(2),
	})
	checkPrice(t, m, 99, 10)
	checkPrice(t, m, 100, 30) // 同一 t 多次记录以最后一次为准
	// 2 的段 [100,3701)：ceil(3601/3600)=2 小时，
	// 第 0 小时价 price(100)=30，第 1 小时价 price(3700)=30，费用 60。
	// 若 (100,40) 未被覆盖则会错算为 40+30=70。
	checkEvents(t, "Terminate(2,3701)", mustTerminate(t, m, 2, 3701), []wantEvent{
		end(2, ReasonUserTerminate, 100, 3701, 60),
		start(3),
	})
	checkBill(t, m, 2, 60)
}

// TestInterruptedRequestRestarts 被中断者之后重新运行是新段，重新计小时。
func TestInterruptedRequestRestarts(t *testing.T) {
	m := mustMarket(t, 2, 10)
	mustRequest(t, m, 1, 50, 0)
	mustRequest(t, m, 2, 40, 0)
	// 2 在 1800 被挤出（段 [0,1800) 中断，0 小时），3700 重新开始新段。
	mustRequest(t, m, 3, 45, 1800)
	checkEvents(t, "Terminate(1,3700)", mustTerminate(t, m, 1, 3700), []wantEvent{
		end(1, ReasonUserTerminate, 0, 3700, 50),
		start(2),
	})
	// 新段 [3700,7200) 用户终止，ceil(3500/3600)=1 小时，价 price(3700)=10。
	checkEvents(t, "Terminate(2,7200)", mustTerminate(t, m, 2, 7200), []wantEvent{
		end(2, ReasonUserTerminate, 3700, 7200, 10),
	})
	// Bill 为两段之和：0 + 10。
	checkBill(t, m, 2, 10)
}

// TestSetCapacityShrinkAndGrow SetCapacity 调小使最低名次者被中断并使出清价上升，
// 调大使等待者开始运行。
func TestSetCapacityShrinkAndGrow(t *testing.T) {
	m := mustMarket(t, 2, 10)
	mustRequest(t, m, 1, 50, 0)
	mustRequest(t, m, 2, 40, 0)
	mustRequest(t, m, 3, 30, 0) // 3 等待，出清价 30（t=0 覆盖初值）
	checkPrice(t, m, 0, 30)
	checkRunning(t, m, 1, 2)

	// 调小到 1：2 被中断，段 [0,7200) floor=2 小时，价 30+30=60；出清价升为 40。
	ev, err := m.SetCapacity(1, 7200)
	if err != nil {
		t.Fatalf("SetCapacity(1,7200) 被拒绝: %v", err)
	}
	checkEvents(t, "SetCapacity(1,7200)", ev, []wantEvent{
		end(2, ReasonMarketInterrupt, 0, 7200, 60),
	})
	checkPrice(t, m, 7200, 40)
	checkRunning(t, m, 1)
	checkBill(t, m, 2, 60)

	// 调大到 3：等待者 2、3 开始运行（按 id 升序），出清价回到 Pmin。
	ev, err = m.SetCapacity(3, 8000)
	if err != nil {
		t.Fatalf("SetCapacity(3,8000) 被拒绝: %v", err)
	}
	checkEvents(t, "SetCapacity(3,8000)", ev, []wantEvent{start(2), start(3)})
	checkPrice(t, m, 8000, 10)
	checkRunning(t, m, 1, 2, 3)
}

// TestTerminateWaitingNoFee 等待中的请求被用户终止不产生费用。
func TestTerminateWaitingNoFee(t *testing.T) {
	m := mustMarket(t, 1, 10)
	mustRequest(t, m, 1, 50, 0)
	mustRequest(t, m, 2, 40, 0) // 2 等待
	checkEvents(t, "Terminate(2,100)", mustTerminate(t, m, 2, 100), nil)
	checkBill(t, m, 2, 0)
	checkRunning(t, m, 1)
}

// TestInvalidConfig 构造参数越界统一以配置非法整体拒绝。
func TestInvalidConfig(t *testing.T) {
	for _, c := range []struct {
		k    int
		pmin int64
	}{
		{0, 10}, {1001, 10}, {-1, 10},
		{1, 0}, {1, -5}, {1, 1_000_000_001},
		{0, 0}, {1001, 1_000_000_001},
	} {
		if _, err := NewMarket(c.k, c.pmin); err != ErrInvalidConfig {
			t.Fatalf("NewMarket(%d,%d) err = %v, want ErrInvalidConfig", c.k, c.pmin, err)
		}
	}
	for _, c := range []struct {
		k    int
		pmin int64
	}{
		{1, 1}, {1000, 1_000_000_000}, {500, 10},
	} {
		if _, err := NewMarket(c.k, c.pmin); err != nil {
			t.Fatalf("NewMarket(%d,%d) 不应被拒绝: %v", c.k, c.pmin, err)
		}
	}
}

// TestRejections 拒绝原因按序只报第一个，且被拒绝的操作不改状态。
func TestRejections(t *testing.T) {
	m := mustMarket(t, 2, 10)
	mustRequest(t, m, 1, 50, 0)
	mustRequest(t, m, 2, 40, 100) // maxT = 100

	cases := []struct {
		label string
		op    func() error
		want  error
	}{
		// Request：参数非法优先于时钟回退与 id 重复。
		{"Request bid 低于底价", func() error { _, e := m.Request(3, 9, 200); return e }, ErrInvalidArgument},
		{"Request bid 超过上限", func() error { _, e := m.Request(3, 1_000_000_001, 200); return e }, ErrInvalidArgument},
		{"Request id 越界", func() error { _, e := m.Request(1_000_000_001, 50, 200); return e }, ErrInvalidArgument},
		{"Request t 越界", func() error { _, e := m.Request(3, 50, 1_000_000_000_000_001); return e }, ErrInvalidArgument},
		{"Request 参数非法优先于时钟回退", func() error { _, e := m.Request(3, 9, 50); return e }, ErrInvalidArgument},
		{"Request 时钟回退", func() error { _, e := m.Request(3, 50, 99); return e }, ErrClockRegression},
		{"Request 时钟回退优先于 id 重复", func() error { _, e := m.Request(1, 50, 99); return e }, ErrClockRegression},
		{"Request id 重复", func() error { _, e := m.Request(1, 60, 100); return e }, ErrDuplicateID},
		// Terminate：参数非法；时钟回退；不存在；已终止。
		{"Terminate id 越界", func() error { _, e := m.Terminate(-1, 200); return e }, ErrInvalidArgument},
		{"Terminate 参数非法优先于时钟回退", func() error { _, e := m.Terminate(-1, 50); return e }, ErrInvalidArgument},
		{"Terminate 时钟回退", func() error { _, e := m.Terminate(9, 99); return e }, ErrClockRegression},
		{"Terminate 时钟回退优先于不存在", func() error { _, e := m.Terminate(9, 50); return e }, ErrClockRegression},
		{"Terminate 不存在", func() error { _, e := m.Terminate(9, 100); return e }, ErrNotFound},
		// SetCapacity：参数非法；时钟回退。
		{"SetCapacity k2 为 0", func() error { _, e := m.SetCapacity(0, 200); return e }, ErrInvalidArgument},
		{"SetCapacity k2 超上限", func() error { _, e := m.SetCapacity(1001, 200); return e }, ErrInvalidArgument},
		{"SetCapacity 参数非法优先于时钟回退", func() error { _, e := m.SetCapacity(0, 50); return e }, ErrInvalidArgument},
		{"SetCapacity 时钟回退", func() error { _, e := m.SetCapacity(1, 99); return e }, ErrClockRegression},
	}
	for _, c := range cases {
		if got := c.op(); got != c.want {
			t.Fatalf("%s: err = %v, want %v", c.label, got, c.want)
		}
	}

	// 终止 1 后，已终止的 id 不得重用；重复终止报已终止。
	mustTerminate(t, m, 1, 100)
	if _, err := m.Request(1, 60, 100); err != ErrDuplicateID {
		t.Fatalf("已终止 id 重用: err = %v, want ErrDuplicateID", err)
	}
	if _, err := m.Terminate(1, 100); err != ErrAlreadyTerminated {
		t.Fatalf("重复终止: err = %v, want ErrAlreadyTerminated", err)
	}

	// 被拒绝的操作不改变状态：maxT 仍为 100，t=100 的操作仍被接受；
	// 运行集合与价格历史未被污染。
	checkRunning(t, m, 2)
	checkPrice(t, m, 100, 10)
	checkEvents(t, "Request(3,45,100)", mustRequest(t, m, 3, 45, 100), []wantEvent{start(3)})
	checkRunning(t, m, 2, 3)
	checkBill(t, m, 1, 10) // 段 [0,100) 用户终止，1 小时 × price(0)=10
}

// TestConcurrent 并发调用等价于某个串行顺序：互斥锁串行化，
// 任何时刻运行请求数不超过 K，费用非负。
func TestConcurrent(t *testing.T) {
	m := mustMarket(t, 5, 1)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := int64(g * 1000)
			for i := int64(0); i < 200; i++ {
				id := base + i
				if _, err := m.Request(id, 1+id%97, i); err != nil {
					continue // 时钟回退等拒绝属正常
				}
				m.Bill(id)
				m.PriceAt(i)
				m.RunningIDs()
				if i%3 == 0 {
					m.Terminate(id, i)
				}
				if i%7 == 0 {
					m.SetCapacity(1+int(i%5), i)
				}
			}
		}(g)
	}
	wg.Wait()
	if got := len(m.RunningIDs()); got > 5 {
		t.Fatalf("运行请求数 %d 超过容量 5", got)
	}
	for id := int64(0); id < 8000; id++ {
		if b := m.Bill(id); b.Sign() < 0 {
			t.Fatalf("Bill(%d) 为负: %v", id, b)
		}
	}
}
