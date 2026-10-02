package burstmeter

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
)

func newTestMeter(t *testing.T, n, b, cap, launch, window, price, debtCap, mode int64) *Meter {
	t.Helper()
	meter, err := NewMeter(n, b, cap, launch, window, price, debtCap, int(mode))
	if err != nil {
		t.Fatalf("NewMeter() error = %v", err)
	}
	return meter
}

func assertTick(t *testing.T, meter *Meter, minute, utilization int64, want TickResult) {
	t.Helper()
	got, err := meter.Tick(minute, utilization)
	if err != nil {
		t.Fatalf("Tick(%d,%d) error = %v", minute, utilization, err)
	}
	if got != want {
		t.Fatalf("Tick(%d,%d) = %+v, want %+v", minute, utilization, got, want)
	}
}

func TestExpiresExactlyAtWindowAndNotOneBefore(t *testing.T) {
	meter := newTestMeter(t, 1, 1, 0, 0, 3, 5, 100, 1)

	assertTick(t, meter, 0, 10, TickResult{Debt: 9})
	assertTick(t, meter, 1, 0, TickResult{Debt: 8})
	assertTick(t, meter, 2, 0, TickResult{Debt: 7})
	t.Logf("判定依据: m=2, age=2 < W=3；Tick 费用为 0，批次仍剩余 7。")

	assertTick(t, meter, 3, 0, TickResult{Fee: 35, Dropped: 1})
	t.Logf("判定依据: m=3, age=3 == W=3；先到期计费 7*5=35，入账不会提前免费偿还。")
}

func TestBillingBeforeCreditDifference(t *testing.T) {
	meter := newTestMeter(t, 2, 10, 100, 30, 3, 5, 100, 1)

	assertTick(t, meter, 0, 50, TickResult{Debt: 50})
	assertTick(t, meter, 1, 0, TickResult{Debt: 30})
	assertTick(t, meter, 2, 10, TickResult{Debt: 30})
	assertTick(t, meter, 3, 0, TickResult{Fee: 50})
	t.Logf("判定依据: 到期先于入账，剩余旧批次 10 计费 50；若先入账，旧批次会被 20 积分免费偿清。")
}

func TestCreditRepaysOldestBatchThenBalance(t *testing.T) {
	meter := newTestMeter(t, 1, 5, 100, 0, 10, 1, 100, 1)

	assertTick(t, meter, 0, 10, TickResult{Debt: 5})
	assertTick(t, meter, 1, 10, TickResult{Debt: 10})
	assertTick(t, meter, 2, 10, TickResult{Debt: 15})
	assertTick(t, meter, 3, 0, TickResult{Debt: 10})

	snapshot := meter.Snapshot()
	if len(snapshot.Batches) != 1 ||
		snapshot.Batches[0] != (DebtBatch{Minute: 2, Remaining: 10}) {
		t.Fatalf("FIFO 偿还后批次 = %+v", snapshot.Batches)
	}
	t.Logf("判定依据: 5 积分先偿最旧批次；m=0、m=1 被顺序偿清后，m=2 仍剩 10。")

	assertTick(t, meter, 4, 0, TickResult{Balance: 0, Debt: 5})
	snapshot = meter.Snapshot()
	if len(snapshot.Batches) != 1 || snapshot.Batches[0] != (DebtBatch{Minute: 2, Remaining: 5}) {
		t.Fatalf("继续 FIFO 偿还后批次 = %+v", snapshot.Batches)
	}

	assertTick(t, meter, 5, 0, TickResult{Balance: 0})
	assertTick(t, meter, 6, 0, TickResult{Balance: 5})
	if meter.Snapshot().Balance != 5 {
		t.Fatalf("全部批次偿清后剩余入账未进入 bal")
	}
}

func TestLaunchCreditsBeforeBalanceAndNeverCapped(t *testing.T) {
	meter := newTestMeter(t, 1, 1, 10, 10, 10, 1, 10, 1)

	assertTick(t, meter, 0, 2, TickResult{Balance: 1})
	snapshot := meter.Snapshot()
	if snapshot.LaunchBalance != 8 || snapshot.Balance != 1 {
		t.Fatalf("启动积分优先级错误: lb=%d bal=%d", snapshot.LaunchBalance, snapshot.Balance)
	}
	t.Logf("判定依据: x=2 先由 lb 支付；lb=8，入账 1 进入 bal，无丢弃。")

	assertTick(t, meter, 1, 10, TickResult{Balance: 0})
	for minute := int64(2); minute <= 10; minute++ {
		assertTick(t, meter, minute, 0, TickResult{Balance: minute - 1})
	}
	assertTick(t, meter, 11, 5, TickResult{Balance: 5})
	snapshot = meter.Snapshot()
	if snapshot.LaunchBalance != 0 || snapshot.Balance != 5 || snapshot.DroppedTotal != 0 {
		t.Fatalf("lb/bal 消耗错误: %+v", snapshot)
	}
	t.Logf("判定依据: lb=10 等于 Cmax，但不受封顶影响；最后一分钟先消耗 lb，再消耗 bal。")
}

func TestDebtCapTruncatesAndRepaymentFreesSameMinuteRoom(t *testing.T) {
	meter := newTestMeter(t, 2, 10, 0, 0, 10, 1, 10, 1)

	assertTick(t, meter, 0, 60, TickResult{Debt: 10, Throttled: 90})
	t.Logf("判定依据: 同分钟入账 20 先被消耗，剩余 need=100，新批次被 Dmax=10 截断。")

	assertTick(t, meter, 1, 20, TickResult{Debt: 10, Throttled: 20})
	t.Logf("判定依据: 入账 20 先偿还旧批次腾出额度，同分钟新 need=40 立即复用其中 10。")

	meter = newTestMeter(t, 2, 10, 100, 30, 10, 1, 40, 1)
	assertTick(t, meter, 0, 50, TickResult{Debt: 40, Throttled: 10})
	t.Logf("判定依据: Dmax=40，新批次截断为 40，多出的 10 计入被限速量。")
}

func TestCapAfterConsumptionAndDroppedCount(t *testing.T) {
	meter := newTestMeter(t, 1, 10, 10, 0, 10, 1, 0, 0)

	assertTick(t, meter, 0, 0, TickResult{Balance: 10})
	assertTick(t, meter, 1, 10, TickResult{Balance: 10})
	t.Logf("判定依据: 入账后瞬时 bal=20，但消耗发生在封顶前，x=10 可用掉。")
	assertTick(t, meter, 2, 0, TickResult{Balance: 10, Dropped: 10})
	if meter.Snapshot().DroppedTotal != 10 {
		t.Fatalf("累计丢弃量错误")
	}
}

func TestStandardModeThrottlesWithoutBatches(t *testing.T) {
	meter := newTestMeter(t, 1, 1, 5, 0, 10, 5, 50, 0)

	assertTick(t, meter, 0, 10, TickResult{Balance: 0, Throttled: 9})
	snapshot := meter.Snapshot()
	if len(snapshot.Batches) != 0 || snapshot.Debt != 0 || snapshot.ThrottledTotal != 9 {
		t.Fatalf("标准模式产生批次或限速量错误: %+v", snapshot)
	}

	fee, err := meter.SetMode(1)
	if err != nil || fee != 0 {
		t.Fatalf("标准切无限 fee=%d err=%v", fee, err)
	}
	assertTick(t, meter, 1, 10, TickResult{Debt: 9})
	t.Logf("判定依据: 标准切无限无费用、无状态副作用；之后不足额才形成批次。")
}

func TestInfiniteToStandardBillsAllBatchesOnce(t *testing.T) {
	meter := newTestMeter(t, 1, 1, 0, 0, 100, 5, 100, 1)

	assertTick(t, meter, 0, 10, TickResult{Debt: 9})
	assertTick(t, meter, 1, 10, TickResult{Debt: 18})

	fee, err := meter.SetMode(0)
	if err != nil || fee != 90 {
		t.Fatalf("SetMode(0) fee=%d err=%v", fee, err)
	}
	snapshot := meter.Snapshot()
	if len(snapshot.Batches) != 0 || snapshot.Debt != 0 || snapshot.BilledTotal != 90 {
		t.Fatalf("切换后批次未清空或费用错误: %+v", snapshot)
	}

	assertTick(t, meter, 2, 0, TickResult{Fee: 0, Dropped: 1})
	if meter.Snapshot().BilledTotal != 90 {
		t.Fatalf("已切换计费的批次被重复到期计费")
	}
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	meter := newTestMeter(t, 1, 1, 10, 2, 10, 5, 10, 1)
	assertTick(t, meter, 0, 5, TickResult{Balance: 0, Debt: 2})
	before := meter.Snapshot()

	if _, err := meter.Tick(-1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("负分钟 err=%v", err)
	}
	if _, err := meter.Tick(0, 101); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("u 越界 err=%v", err)
	}
	if _, err := meter.Tick(0, 0); !errors.Is(err, ErrSequenceRewind) {
		t.Fatalf("序号回退 err=%v", err)
	}
	if _, err := meter.Tick(2, 0); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("序号缺口 err=%v", err)
	}
	if _, err := meter.SetMode(2); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("mode 越界 err=%v", err)
	}

	after := meter.Snapshot()
	if after.NextMinute != before.NextMinute ||
		after.Balance != before.Balance ||
		after.LaunchBalance != before.LaunchBalance ||
		after.Debt != before.Debt ||
		after.BilledTotal != before.BilledTotal {
		t.Fatalf("拒绝操作改变状态: before=%+v after=%+v", before, after)
	}
}

func TestInvalidConfigurationRejected(t *testing.T) {
	invalidCases := [][]int64{
		{0, 1, 0, 0, 1, 0, 0},
		{65, 1, 0, 0, 1, 0, 0},
		{1, 0, 0, 0, 1, 0, 0},
		{1, 101, 0, 0, 1, 0, 0},
		{1, 1, -1, 0, 1, 0, 0},
		{1, 1, 0, 1, 1, 0, 0},
		{1, 1, 0, 0, 0, 0, 0},
		{1, 1, 0, 0, 1, -1, 0},
		{1, 1, 0, 0, 1, 0, -1},
	}
	for _, args := range invalidCases {
		_, err := NewMeter(args[0], args[1], args[2], args[3], args[4], args[5], args[6], 0)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}

	_, err := NewMeter(1, 1, 0, 0, 1, 0, 0, 2)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("初始模式越界 err=%v", err)
	}
}

type naiveBatch struct {
	minute    int64
	remaining int64
}

type simConfig struct {
	n, b, cap, launch, window, price, debtCap, mode int64
}

type naiveMeter struct {
	n, e, cap, lb, window, price, debtCap int64
	bal                                   int64
	batches                               []naiveBatch
	dropped                               int64
	throttled                             int64
	billed                                int64
	credited                              int64
	balConsumed                           int64
	lbConsumed                            int64
	repaid                                int64
	next                                  int64
	mode                                  int
}

func newNaive(cfg simConfig) *naiveMeter {
	return &naiveMeter{
		n:       cfg.n,
		e:       cfg.n * cfg.b,
		cap:     cfg.cap,
		lb:      cfg.launch,
		window:  cfg.window,
		price:   cfg.price,
		debtCap: cfg.debtCap,
		mode:    int(cfg.mode),
	}
}

func (m *naiveMeter) debt() int64 {
	total := int64(0)
	for _, batch := range m.batches {
		total += batch.remaining
	}
	return total
}

func (m *naiveMeter) tick(minute, utilization int64) (TickResult, error) {
	if minute < 0 || utilization < 0 || utilization > 100 {
		return TickResult{}, ErrInvalidArgument
	}
	if minute < m.next {
		return TickResult{}, ErrSequenceRewind
	}
	if minute > m.next {
		return TickResult{}, ErrSequenceGap
	}

	result := TickResult{}
	kept := make([]naiveBatch, 0, len(m.batches))
	for _, batch := range m.batches {
		if minute-batch.minute >= m.window {
			result.Fee += batch.remaining * m.price
			continue
		}
		kept = append(kept, batch)
	}
	m.batches = kept

	credit := m.e
	for i := 0; i < len(m.batches) && credit > 0; i++ {
		payment := min(credit, m.batches[i].remaining)
		m.batches[i].remaining -= payment
		credit -= payment
		m.repaid += payment
	}
	open := m.batches[:0]
	for _, batch := range m.batches {
		if batch.remaining > 0 {
			open = append(open, batch)
		}
	}
	m.batches = open
	m.credited += m.e
	m.bal += credit

	need := m.n * utilization
	launchUsed := min(m.lb, need)
	m.lb -= launchUsed
	m.lbConsumed += launchUsed
	need -= launchUsed

	balanceUsed := min(m.bal, need)
	m.bal -= balanceUsed
	m.balConsumed += balanceUsed
	need -= balanceUsed

	if need > 0 {
		room := m.debtCap - m.debt()
		if m.mode == 1 && room > 0 {
			added := min(need, room)
			m.batches = append(m.batches, naiveBatch{
				minute:    minute,
				remaining: added,
			})
			need -= added
		}
		result.Throttled = need
		m.throttled += need
	}

	if m.bal > m.cap {
		result.Dropped = m.bal - m.cap
		m.bal = m.cap
		m.dropped += result.Dropped
	}

	m.billed += result.Fee
	m.next = minute + 1
	result.Balance = m.bal
	result.Debt = m.debt()
	return result, nil
}

func (m *naiveMeter) setMode(mode int) (int64, error) {
	if mode != 0 && mode != 1 {
		return 0, ErrInvalidArgument
	}
	fee := int64(0)
	if m.mode == 1 && mode == 0 {
		fee = m.debt() * m.price
		m.batches = nil
		m.billed += fee
	}
	m.mode = mode
	return fee, nil
}

func TestRandomSequencesMatchNaiveSimulation(t *testing.T) {
	seed := uint64(20261002)
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))

	for sequence := 0; sequence < 2000; sequence++ {
		cfg := simConfig{
			n:       int64(rng.IntN(8) + 1),
			b:       int64(rng.IntN(100) + 1),
			cap:     int64(rng.IntN(201)),
			window:  int64(rng.IntN(8) + 1),
			price:   int64(rng.IntN(6)),
			debtCap: int64(rng.IntN(121)),
			mode:    int64(rng.IntN(2)),
		}
		cfg.launch = int64(rng.IntN(int(cfg.cap) + 1))

		meter, err := NewMeter(cfg.n, cfg.b, cfg.cap, cfg.launch, cfg.window, cfg.price, cfg.debtCap, int(cfg.mode))
		if err != nil {
			t.Fatalf("随机配置构造失败 cfg=%+v err=%v", cfg, err)
		}
		reference := newNaive(cfg)

		var log strings.Builder
		recordFailure := func(format string, args ...any) {
			t.Fatalf("随机序列 #%d\ncfg=%+v\n操作日志:\n%s判定依据: %s",
				sequence, cfg, log.String(), sprintf(format, args...))
		}

		for step := 0; step < 80; step++ {
			choice := rng.IntN(10)
			switch {
			case choice < 6:
				minute := reference.next
				utilization := int64(rng.IntN(101))

				got, gotErr := meter.Tick(minute, utilization)
				want, wantErr := reference.tick(minute, utilization)
				writeTickLog(&log, minute, utilization, got, gotErr, want, wantErr)
				if !sameError(gotErr, wantErr) || got != want {
					recordFailure("Tick 输入、返回值或错误必须与朴素模拟一致")
				}
			case choice == 6:
				minute := reference.next + int64(rng.IntN(3)+1)
				utilization := int64(rng.IntN(101))

				got, gotErr := meter.Tick(minute, utilization)
				want, wantErr := reference.tick(minute, utilization)
				writeTickLog(&log, minute, utilization, got, gotErr, want, wantErr)
				if !errors.Is(gotErr, ErrSequenceGap) || !sameError(gotErr, wantErr) || got != want {
					recordFailure("m>next 必须只报序号缺口且不改变状态")
				}
			case choice == 7:
				if reference.next == 0 {
					continue
				}
				minute := reference.next - int64(rng.IntN(3)+1)
				if minute < 0 {
					minute = 0
				}
				utilization := int64(rng.IntN(101))

				got, gotErr := meter.Tick(minute, utilization)
				want, wantErr := reference.tick(minute, utilization)
				writeTickLog(&log, minute, utilization, got, gotErr, want, wantErr)
				if !errors.Is(gotErr, ErrSequenceRewind) || !sameError(gotErr, wantErr) || got != want {
					recordFailure("m<next 必须只报序号回退且不改变状态")
				}
			case choice == 8:
				minute := reference.next
				utilization := int64(101 + rng.IntN(10))

				got, gotErr := meter.Tick(minute, utilization)
				want, wantErr := reference.tick(minute, utilization)
				writeTickLog(&log, minute, utilization, got, gotErr, want, wantErr)
				if !errors.Is(gotErr, ErrInvalidArgument) || !sameError(gotErr, wantErr) || got != want {
					recordFailure("u 越界必须先于序号检查且不改变状态")
				}
			default:
				mode := rng.IntN(3)
				gotFee, gotErr := meter.SetMode(mode)
				wantFee, wantErr := reference.setMode(mode)
				if !sameError(gotErr, wantErr) || gotFee != wantFee {
					recordFailure("SetMode(mode=%d) fee=%d err=%v，朴素模拟 fee=%d err=%v",
						mode, gotFee, gotErr, wantFee, wantErr)
				}
				writeModeLog(&log, mode, gotFee, gotErr)
			}

			if !snapshotsEqual(meter.Snapshot(), reference) {
				recordFailure("操作后完整状态与朴素模拟不一致")
			}
			assertInvariants(meter, t)
		}

		t.Logf("随机序列 #%d 通过，cfg=%+v；80 个操作的输入、输出与判定依据均已逐条记录。\n%s",
			sequence, cfg, log.String())
	}
}

func TestConcurrentOperations(t *testing.T) {
	meter := newTestMeter(t, 4, 25, 500, 100, 10, 5, 300, 1)
	var wg sync.WaitGroup

	for worker := 0; worker < 64; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				meter.Snapshot()
				if _, err := meter.SetMode(i % 2); err != nil {
					t.Errorf("SetMode: %v", err)
					return
				}
				if _, err := meter.Tick(0, int64(id%101)); err != nil &&
					!errors.Is(err, ErrSequenceRewind) {
					t.Errorf("Tick: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	snapshot := meter.Snapshot()
	if snapshot.NextMinute != 1 || snapshot.Balance < 0 || snapshot.Balance > 500 || snapshot.Debt > 300 {
		t.Fatalf("并发后状态不变量错误: %+v", snapshot)
	}
	t.Logf("判定依据: 64 个 goroutine 并发查询、切换模式和 Tick；结果必须等价于某串行顺序，race 检测无数据竞争。")
}

func writeTickLog(builder *strings.Builder, minute, utilization int64, got TickResult, gotErr error, want TickResult, wantErr error) {
	builder.WriteString(sprintf(
		"input Tick(m=%d,u=%d) output got=(fee=%d,bal=%d,debt=%d,dropped=%d,throttled=%d,err=%v) naive=(fee=%d,bal=%d,debt=%d,dropped=%d,throttled=%d,err=%v)\n",
		minute, utilization,
		got.Fee, got.Balance, got.Debt, got.Dropped, got.Throttled, gotErr,
		want.Fee, want.Balance, want.Debt, want.Dropped, want.Throttled, wantErr,
	))
}

func writeModeLog(builder *strings.Builder, mode int, fee int64, err error) {
	builder.WriteString(sprintf("input SetMode(mode=%d) output fee=%d err=%v\n", mode, fee, err))
}

func sameError(got error, want error) bool {
	switch {
	case got == nil || want == nil:
		return got == want
	case errors.Is(want, ErrInvalidArgument):
		return errors.Is(got, ErrInvalidArgument)
	case errors.Is(want, ErrSequenceRewind):
		return errors.Is(got, ErrSequenceRewind)
	case errors.Is(want, ErrSequenceGap):
		return errors.Is(got, ErrSequenceGap)
	default:
		return got.Error() == want.Error()
	}
}

func snapshotsEqual(got Snapshot, want *naiveMeter) bool {
	wantDebt := want.debt()
	if got.Balance != want.bal ||
		got.LaunchBalance != want.lb ||
		got.Debt != wantDebt ||
		got.DroppedTotal != want.dropped ||
		got.ThrottledTotal != want.throttled ||
		got.BilledTotal != want.billed ||
		got.CreditedTotal != want.credited ||
		got.BalanceConsumedTotal != want.balConsumed ||
		got.LaunchConsumedTotal != want.lbConsumed ||
		got.RepaidTotal != want.repaid ||
		got.NextMinute != want.next ||
		got.Mode != want.mode ||
		len(got.Batches) != len(want.batches) {
		return false
	}
	for i := range want.batches {
		if got.Batches[i].Minute != want.batches[i].minute ||
			got.Batches[i].Remaining != want.batches[i].remaining {
			return false
		}
	}
	return true
}

func assertInvariants(meter *Meter, t *testing.T) {
	t.Helper()
	snapshot := meter.Snapshot()

	if snapshot.Balance < 0 || snapshot.Balance > meter.balanceCap {
		t.Fatalf("余额越界: %+v", snapshot)
	}
	if snapshot.Debt < 0 || snapshot.Debt > meter.debtCap {
		t.Fatalf("欠额越界: %+v", snapshot)
	}
	if snapshot.Mode == 0 && (len(snapshot.Batches) != 0 || snapshot.Debt != 0) {
		t.Fatalf("标准模式存在批次: %+v", snapshot)
	}
	for i, batch := range snapshot.Batches {
		if batch.Remaining <= 0 {
			t.Fatalf("批次剩余欠额非正: %+v", snapshot.Batches)
		}
		if i > 0 && batch.Minute <= snapshot.Batches[i-1].Minute {
			t.Fatalf("批次产生分钟未严格递增: %+v", snapshot.Batches)
		}
	}

	conservation := snapshot.Balance + snapshot.BalanceConsumedTotal + snapshot.RepaidTotal + snapshot.DroppedTotal
	if conservation != snapshot.CreditedTotal {
		t.Fatalf("入账守恒不成立: credited=%d bal=%d balConsumed=%d repaid=%d dropped=%d",
			snapshot.CreditedTotal, snapshot.Balance, snapshot.BalanceConsumedTotal,
			snapshot.RepaidTotal, snapshot.DroppedTotal)
	}
}

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
