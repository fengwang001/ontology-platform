package burstmeter

import (
	"errors"
	"flag"
	"runtime"
	"sync"
	"testing"
)

var verboseLog = flag.Bool("verbose", false, "打印逐条输入/输出与判定依据日志")

func logf(t *testing.T, format string, args ...any) {
	if *verboseLog {
		t.Helper()
		t.Logf(format, args...)
	}
}

func mustNew(t *testing.T, c Config) *Meter {
	t.Helper()
	m, err := New(c)
	if err != nil {
		t.Fatalf("New(%+v) 意外失败: %v", c, err)
	}
	return m
}

// 题目给出的示例序列。
func TestSpecExample(t *testing.T) {
	m := mustNew(t, Config{N: 2, B: 10, Cmax: 100, L: 30, W: 3, Price: 5, Dmax: 100, InitialMode: ModeInfinite})

	r, err := m.Tick(0, 50)
	if err != nil {
		t.Fatal(err)
	}
	want := TickResult{Fee: 0, Balance: 0, Debt: 50, Dropped: 0, Throttled: 0}
	if r != want {
		t.Fatalf("Tick(0,50) = %+v, want %+v", r, want)
	}
	if got := m.LaunchBalance(); got != 0 {
		t.Fatalf("lb = %d, want 0", got)
	}

	r, err = m.Tick(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	want = TickResult{Fee: 0, Balance: 0, Debt: 30}
	if r != want {
		t.Fatalf("Tick(1,0) = %+v, want %+v", r, want)
	}

	r, err = m.Tick(2, 10)
	if err != nil {
		t.Fatal(err)
	}
	want = TickResult{Fee: 0, Balance: 0, Debt: 30}
	if r != want {
		t.Fatalf("Tick(2,10) = %+v, want %+v（旧批剩10 + 新批20）", r, want)
	}

	r, err = m.Tick(3, 0)
	if err != nil {
		t.Fatal(err)
	}
	want = TickResult{Fee: 50, Balance: 0, Debt: 0}
	if r != want {
		t.Fatalf("Tick(3,0) = %+v, want %+v：必须先到期后入账，否则旧批被免费偿清", r, want)
	}
}

// Dmax 截断：只追加 40，另 10 计入被限速量；同分钟入账腾出额度可立即复用。
func TestDebtCapTruncation(t *testing.T) {
	m := mustNew(t, Config{N: 2, B: 10, Cmax: 100, L: 30, W: 3, Price: 5, Dmax: 40, InitialMode: ModeInfinite})
	r, err := m.Tick(0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if r.Debt != 40 || r.Throttled != 10 || r.Balance != 0 {
		t.Fatalf("截断结果 = %+v, want Debt=40 Throttled=10 Balance=0", r)
	}
	batches := m.Batches()
	if len(batches) != 1 || batches[0] != (Batch{Minute: 0, Remaining: 40}) {
		t.Fatalf("批次 = %+v, want [(0,40)]", batches)
	}

	// 入账20偿旧批（40->20），x=100 且 lb=0/bal=0 -> need=100，
	// room=40-20=20 -> 追加 (1,20)，限速 80；欠额总量仍为 40。
	r, err = m.Tick(1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if r.Debt != 40 || r.Throttled != 80 {
		t.Fatalf("同分钟入账腾出额度复用错误: %+v, want Debt=40 Throttled=80", r)
	}
	batches = m.Batches()
	if len(batches) != 2 || batches[0] != (Batch{Minute: 0, Remaining: 20}) ||
		batches[1] != (Batch{Minute: 1, Remaining: 20}) {
		t.Fatalf("批次 = %+v, want [(0,20) (1,20)]", batches)
	}
}

// 到期边界：m-gen == W-1 不计费，== W 立即计费。
func TestExpiryBoundary(t *testing.T) {
	m := mustNew(t, Config{N: 1, B: 1, Cmax: 100, L: 0, W: 3, Price: 7, Dmax: 1000, InitialMode: ModeInfinite})
	if r, _ := m.Tick(0, 10); r.Debt != 9 { // 入账1，x=10
		t.Fatalf("setup: Debt=%d want 9", r.Debt)
	}
	if r, _ := m.Tick(1, 0); r.Fee != 0 || r.Debt != 8 {
		t.Fatalf("m=1: %+v want Fee=0 Debt=8", r)
	}
	r, _ := m.Tick(2, 0)
	if r.Fee != 0 || r.Debt != 7 {
		t.Fatalf("m=2（差 1 不到期）: %+v want Fee=0 Debt=7", r)
	}
	r, _ = m.Tick(3, 0)
	if r.Fee != 49 || r.Debt != 0 { // 7*7：先到期，入账的1不再免费偿旧批
		t.Fatalf("m=3（恰等即到期）: %+v want Fee=49 Debt=0；若先入账只会是42", r)
	}
}

// FIFO：入账先偿最旧批次，再偿较新批次；剩余才进 bal。
func TestFIFOOrderAndBalanceCredit(t *testing.T) {
	m := mustNew(t, Config{N: 1, B: 1, Cmax: 1000, L: 0, W: 100, Price: 2, Dmax: 1000, InitialMode: ModeInfinite})
	m.Tick(0, 4) // 入账1，x=4 -> (0,3)
	m.Tick(1, 4) // 偿1（旧批剩2），x=4 全欠 -> (1,4)，欠额总 6

	// 直接修改内部参数模拟“大额入账”分钟（同构于 n=1,b=10）。
	m.b = 10
	r, err := m.Tick(2, 0) // 入账10：先偿 (0,2) 再偿 (1,4)，剩 4 进 bal
	if err != nil {
		t.Fatal(err)
	}
	if r.Debt != 0 || r.Balance != 4 {
		t.Fatalf("FIFO/余额入账: %+v want Debt=0 Balance=4", r)
	}
	if len(m.Batches()) != 0 {
		t.Fatalf("批次应为空: %+v", m.Batches())
	}
}

// 启动积分先于余额消耗、不被封顶；L 恰等于 Cmax。
func TestLaunchBalancePriority(t *testing.T) {
	m := mustNew(t, Config{N: 1, B: 5, Cmax: 30, L: 30, W: 100, Price: 1, Dmax: 1000, InitialMode: ModeStandard})
	r, _ := m.Tick(0, 20) // x=20 全由 lb 支付；入账5进 bal
	if m.LaunchBalance() != 10 || r.Balance != 5 {
		t.Fatalf("lb=%d bal=%d, want lb=10 bal=5", m.LaunchBalance(), r.Balance)
	}
	r, _ = m.Tick(1, 10) // lb 剩10全额支付，bal 不动
	if m.LaunchBalance() != 0 || r.Balance != 10 {
		t.Fatalf("lb=%d bal=%d, want lb=0 bal=10", m.LaunchBalance(), r.Balance)
	}
	var last TickResult
	for min := int64(2); min <= 5; min++ { // 入账 4*5 -> bal 30
		last, _ = m.Tick(min, 0)
	}
	if last.Balance != 30 || last.Dropped != 0 {
		t.Fatalf("bal=%d dropped=%d, want 30/0（lb 不被封顶）", last.Balance, last.Dropped)
	}
}

// 封顶发生在消耗之后：同分钟入账后可超上限，消耗用掉后不丢弃。
func TestCapAfterConsumption(t *testing.T) {
	m := mustNew(t, Config{N: 1, B: 10, Cmax: 15, L: 0, W: 100, Price: 1, Dmax: 1000, InitialMode: ModeStandard})
	m.Tick(0, 0)          // bal=10
	m.Tick(1, 0)          // bal=15，丢5
	r, _ := m.Tick(2, 12) // 入账10->25，消耗12->13，无丢弃
	if r.Balance != 13 || r.Dropped != 0 {
		t.Fatalf("消耗先于封顶: %+v want Balance=13 Dropped=0", r)
	}
	earned, consumed, repaid, dropped, _, _ := m.Totals()
	if earned != 30 || consumed != 12 || repaid != 0 || dropped != 5 {
		t.Fatalf("累计值 earned=%d consumed=%d repaid=%d dropped=%d, want 30/12/0/5",
			earned, consumed, repaid, dropped)
	}
	if earned != r.Balance+consumed+repaid+dropped {
		t.Fatal("入账守恒不成立")
	}
}

// 标准模式：need 直接被限速，不产生批次。
func TestStandardModeThrottle(t *testing.T) {
	m := mustNew(t, Config{N: 2, B: 5, Cmax: 10, L: 0, W: 3, Price: 5, Dmax: 100, InitialMode: ModeStandard})
	r, _ := m.Tick(0, 50) // 入账10，x=100，限速90
	if r.Throttled != 90 || r.Debt != 0 || r.Balance != 0 || len(m.Batches()) != 0 {
		t.Fatalf("标准模式结果 %+v batches=%v", r, m.Batches())
	}
}

// 无限切标准立即计费且不重复到期；标准切无限无副作用。
func TestModeSwitchSettlement(t *testing.T) {
	m := mustNew(t, Config{N: 1, B: 1, Cmax: 100, L: 0, W: 1000, Price: 3, Dmax: 1000, InitialMode: ModeInfinite})
	m.Tick(0, 10) // (0,9)
	m.Tick(1, 10) // 偿1 -> (0,8)，x=10 全欠 -> (1,10)，总18

	fee, err := m.SetMode(ModeStandard)
	if err != nil || fee != 18*3 {
		t.Fatalf("切换费用 fee=%d err=%v, want %d", fee, err, 18*3)
	}
	if m.Debt() != 0 || len(m.Batches()) != 0 {
		t.Fatalf("切换后批次未清空: debt=%d batches=%v", m.Debt(), m.Batches())
	}
	fee, _ = m.SetMode(ModeStandard) // 同模式无费用
	if fee != 0 {
		t.Fatalf("同模式切换费用 = %d, want 0", fee)
	}
	for min := int64(2); min <= 1100; min++ {
		r, err := m.Tick(min, 0)
		if err != nil {
			t.Fatal(err)
		}
		if r.Fee != 0 {
			t.Fatalf("m=%d 出现重复到期费用 %d", min, r.Fee)
		}
	}
	if _, _, _, _, _, totalFee := m.Totals(); totalFee != 18*3 {
		t.Fatalf("总费用 %d, want %d（不得重复计费）", totalFee, 18*3)
	}

	m2 := mustNew(t, Config{N: 1, B: 1, Cmax: 10, L: 0, W: 3, Price: 2, Dmax: 100, InitialMode: ModeStandard})
	fee, _ = m2.SetMode(ModeInfinite)
	if fee != 0 || m2.Mode() != ModeInfinite || m2.Balance() != 0 || m2.Next() != 0 {
		t.Fatalf("标准切无限产生副作用: fee=%d mode=%d", fee, m2.Mode())
	}
}

func reasonOf(err error) RejectReason {
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return ReasonInvalidArgument
	case errors.Is(err, ErrSequenceFallback):
		return ReasonSequenceFallback
	case errors.Is(err, ErrSequenceGap):
		return ReasonSequenceGap
	default:
		return ReasonNone
	}
}

// 构造参数越界整体拒绝。
func TestInvalidConfig(t *testing.T) {
	good := Config{N: 2, B: 10, Cmax: 100, L: 30, W: 3, Price: 5, Dmax: 100, InitialMode: ModeInfinite}
	bad := []func(*Config){
		func(c *Config) { c.N = 0 },
		func(c *Config) { c.N = 65 },
		func(c *Config) { c.B = 0 },
		func(c *Config) { c.B = 101 },
		func(c *Config) { c.Cmax = -1 },
		func(c *Config) { c.Cmax = 1_000_000_000_001 },
		func(c *Config) { c.L = 101 },
		func(c *Config) { c.L = -1 },
		func(c *Config) { c.W = 0 },
		func(c *Config) { c.W = 1_000_001 },
		func(c *Config) { c.Price = -1 },
		func(c *Config) { c.Price = 1_000_001 },
		func(c *Config) { c.Dmax = -1 },
		func(c *Config) { c.Dmax = 1_000_000_000_001 },
		func(c *Config) { c.InitialMode = 2 },
	}
	for i, mutate := range bad {
		c := good
		mutate(&c)
		if _, err := New(c); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("非法配置 #%d (%+v) err=%v, want ErrInvalidConfig", i, c, err)
		}
	}
	// 边界合法值。
	for _, c := range []Config{
		{N: 1, B: 1, Cmax: 0, L: 0, W: 1, Price: 0, Dmax: 0, InitialMode: 0},
		{N: 64, B: 100, Cmax: 1_000_000_000_000, L: 1_000_000_000_000, W: 1_000_000, Price: 1_000_000, Dmax: 1_000_000_000_000, InitialMode: 1},
	} {
		if _, err := New(c); err != nil {
			t.Fatalf("合法边界配置被拒: %+v %v", c, err)
		}
	}
}

type snapshot struct {
	bal, lb, debt, next int64
	mode                int
	batches             []Batch
}

func snap(m *Meter) snapshot {
	return snapshot{m.Balance(), m.LaunchBalance(), m.Debt(), m.Next(), m.Mode(), m.Batches()}
}

func (s snapshot) equal(o snapshot) bool {
	return s.bal == o.bal && s.lb == o.lb && s.debt == o.debt && s.next == o.next &&
		s.mode == o.mode && batchesEqual(s.batches, o.batches)
}

func batchesEqual(a, b []Batch) bool {
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

// Tick 拒绝顺序：参数非法 > 序号回退 > 序号缺口；拒绝不改状态。
func TestTickRejectionOrderAndNoStateChange(t *testing.T) {
	m := mustNew(t, Config{N: 1, B: 1, Cmax: 10, L: 0, W: 3, Price: 2, Dmax: 100, InitialMode: ModeInfinite})
	m.Tick(0, 10)

	// u 越界即使同时序号回退，也只报参数非法。
	if _, err := m.Tick(-1, 200); reasonOf(err) != ReasonInvalidArgument {
		t.Fatalf("(-1,200) reason=%v, want 参数非法", reasonOf(err))
	}
	if _, err := m.Tick(0, 200); reasonOf(err) != ReasonInvalidArgument {
		t.Fatalf("(0,200) reason=%v, want 参数非法（优先于回退）", reasonOf(err))
	}
	before := snap(m)
	if _, err := m.Tick(0, 50); reasonOf(err) != ReasonSequenceFallback {
		t.Fatalf("(0,50) reason=%v, want 序号回退", reasonOf(err))
	}
	if got := snap(m); !got.equal(before) {
		t.Fatalf("回退拒绝改变了状态: before=%+v after=%+v", before, got)
	}
	if _, err := m.Tick(5, 0); reasonOf(err) != ReasonSequenceGap {
		t.Fatalf("(5,0) reason=%v, want 序号缺口", reasonOf(err))
	}
	if got := snap(m); !got.equal(before) {
		t.Fatalf("缺口拒绝改变了状态: before=%+v after=%+v", before, got)
	}
	// next 仍是 1，连续分钟可正常处理。
	r, err := m.Tick(1, 0)
	if err != nil {
		t.Fatalf("拒绝后正常 Tick 失败: %v", err)
	}
	// Tick(0,10)：入账1进 bal，x=10 先耗掉 bal 1 再欠 9 -> before.bal=0；
	// Tick(1,0)：入账 1 偿批次 (9->8)，无消耗无到期，bal 仍为 0。
	if r.Balance != 0 || r.Debt != 8 {
		t.Fatalf("拒绝后正常 Tick 结果异常: %+v want Balance=0 Debt=8", r)
	}

	// SetMode 越界拒绝且不改状态。
	before = snap(m)
	if _, err := m.SetMode(2); reasonOf(err) != ReasonInvalidArgument {
		t.Fatalf("SetMode(2) reason=%v", reasonOf(err))
	}
	if got := snap(m); !got.equal(before) {
		t.Fatalf("SetMode 拒绝改变了状态")
	}
}

// 并发调用：race 检测下串行可执行，结果不崩溃、不变量保持。
func TestConcurrentAccess(t *testing.T) {
	m := mustNew(t, Config{N: 4, B: 20, Cmax: 500, L: 100, W: 5, Price: 3, Dmax: 500, InitialMode: ModeInfinite})
	var wg sync.WaitGroup
	done := make(chan struct{})

	wg.Add(1)
	go func() { // 唯一的分钟写入者，严格递增
		defer wg.Done()
		for min := int64(0); min < 2000; min++ {
			if _, err := m.Tick(min, int(min%101)); err != nil {
				t.Errorf("Tick(%d): %v", min, err)
				return
			}
		}
	}()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 5000; round++ {
				select {
				case <-done:
					return
				default:
					_ = m.Balance()
					_ = m.LaunchBalance()
					_ = m.Debt()
					_ = m.Batches()
					_, _, _, _, _, _ = m.Totals()
					_ = m.Next()
					_ = m.Mode()
				}
				runtime.Gosched()
			}
		}()
	}
	wg.Wait()
	close(done)
	if m.Next() != 2000 {
		t.Fatalf("Next=%d want 2000", m.Next())
	}
	if m.Debt() > 500 || m.Balance() > 500 || m.Balance() < 0 {
		t.Fatalf("不变量被破坏: bal=%d debt=%d", m.Balance(), m.Debt())
	}
}
