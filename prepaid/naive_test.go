package prepaid

// 本文件实现一个按规则逐事件写成的独立朴素模型，
// 用于与正式实现对照大量随机操作序列。
// 朴素模型刻意使用低效的线性扫描（节假日、电价），
// 并与正式实现采用不同的代码结构，以提高对照的独立性。

import (
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"
)

type naiveAccount struct {
	cfg      Config
	clock    int64
	balance  int64
	arrears  int64
	state    State
	emUsed   int64
	emPeriod int64

	hasRead bool
	lastT   int64
	lastE   int64

	priceT []int64
	priceV []int64

	warned   bool
	execAt   int64
	since    int64
	deadline int64

	pCharges int64
	pWarn    int64
	pCutDur  int64

	totR int64
	totG int64
	totC int64
	totP int64

	holidays []int64
	events   []Event
}

func newNaive(cfg Config) *naiveAccount {
	return &naiveAccount{
		cfg:      cfg,
		state:    StateSupplyOn,
		emPeriod: -1,
		priceT:   []int64{0},
		priceV:   []int64{cfg.InitialPriceMilli},
	}
}

func (n *naiveAccount) log(e Event) { n.events = append(n.events, e) }

func (n *naiveAccount) priceAt(t int64) int64 {
	for i := len(n.priceT) - 1; i >= 0; i-- {
		if n.priceT[i] <= t {
			return n.priceV[i]
		}
	}
	panic("no price")
}

func (n *naiveAccount) isHoliday(day int64) bool {
	for _, h := range n.holidays {
		if h == day {
			return true
		}
	}
	return false
}

func (n *naiveAccount) friendly(t int64) bool {
	day := t / secondsPerDay
	if n.isHoliday(day) {
		return true
	}
	if n.cfg.RestWeekdays[(day+n.cfg.EpochWeekday)%7] {
		return true
	}
	sec := t % secondsPerDay
	s, e := n.cfg.FriendlyStartSec, n.cfg.FriendlyEndSec
	if s == e {
		return false
	}
	if s < e {
		return sec >= s && sec < e
	}
	return sec >= s || sec < e
}

// friendlyEnd 朴素实现：从 t 起逐步扩张，每次跳到当前所在友好区间的
// 结束点，直到落出不友好为止（测试配置保证不会无限友好）。
func (n *naiveAccount) friendlyEnd(t int64) int64 {
	end := t
	for n.friendly(end) {
		day, sec := end/secondsPerDay, end%secondsPerDay
		if n.isHoliday(day) || n.cfg.RestWeekdays[(day+n.cfg.EpochWeekday)%7] {
			end = (day + 1) * secondsPerDay
			continue
		}
		s, e := n.cfg.FriendlyStartSec, n.cfg.FriendlyEndSec
		switch {
		case s < e:
			end = day*secondsPerDay + e
		case sec >= s:
			end = (day+1)*secondsPerDay + e
		default:
			end = day*secondsPerDay + e
		}
	}
	return end
}

func (n *naiveAccount) deferCutoff(t int64) int64 {
	if n.friendly(t) {
		return n.friendlyEnd(t)
	}
	return t
}

func (n *naiveAccount) warnCheck(t int64) {
	below := n.balance < n.cfg.WarnThreshold
	if below && !n.warned {
		n.pWarn++
		n.log(Event{Kind: EvWarning, Time: t, Balance: n.balance, Arrears: n.arrears})
	}
	n.warned = below
}

func (n *naiveAccount) advance(T int64) {
	for {
		// 收集所有不晚于 T 的待处理时点，取最早者；并列时状态迁移优先。
		next := T + 1
		what := 0
		if n.state == StatePendingCutoff && n.execAt <= T && n.execAt < next {
			next, what = n.execAt, 1
		}
		if n.state == StatePendingRestore && n.deadline < T && n.deadline < next {
			next, what = n.deadline, 2
		}
		if b := (n.clock/n.cfg.PeriodLength + 1) * n.cfg.PeriodLength; b <= T && b < next {
			next, what = b, 3
		}
		if what == 0 {
			break
		}
		n.clock = next
		switch what {
		case 1:
			n.state = StateCutOff
			if n.balance < 0 {
				n.arrears -= n.balance
				n.balance = 0
			}
			n.since = n.clock
			n.log(Event{Kind: EvCutoffExecuted, Time: n.clock, Balance: n.balance, Arrears: n.arrears})
		case 2:
			n.state = StateCutOff
			n.since = n.clock
			n.log(Event{Kind: EvRestoreExpired, Time: n.clock, Balance: n.balance, Arrears: n.arrears})
		case 3:
			if n.state == StateCutOff {
				n.pCutDur += n.clock - n.since
				n.since = n.clock
			}
			n.log(Event{
				Kind:           EvPeriodSummary,
				Time:           n.clock,
				Period:         n.clock/n.cfg.PeriodLength - 1,
				Charges:        n.pCharges,
				Warnings:       n.pWarn,
				CutoffDuration: n.pCutDur,
			})
			n.pCharges, n.pWarn, n.pCutDur = 0, 0, 0
		}
	}
	n.clock = T
}

func (n *naiveAccount) addReading(t, energy int64) error {
	if t < 0 || energy < 0 {
		return newErr(ErrInvalidParam)
	}
	if t < n.clock {
		return newErr(ErrClockRegression)
	}
	if n.hasRead && t <= n.lastT {
		return newErr(ErrTimeOrder)
	}
	if n.hasRead && energy < n.lastE {
		return newErr(ErrReadingRollback)
	}
	n.advance(t)
	if n.hasRead {
		delta := energy - n.lastE
		if delta > 0 {
			price := n.priceAt(n.lastT)
			amount := delta * price / 1000
			if amount > 0 {
				n.balance -= amount
				n.totC += amount
				n.pCharges += amount
				n.log(Event{Kind: EvCharge, Time: t, Energy: delta, Amount: amount, Price: price, Balance: n.balance, Arrears: n.arrears})
			}
			if n.state == StateCutOff {
				n.log(Event{Kind: EvOffGridUsage, Time: t, Energy: delta, Balance: n.balance, Arrears: n.arrears})
			}
			if amount > 0 {
				n.warnCheck(t)
				switch n.state {
				case StateSupplyOn:
					if n.balance < 0 {
						n.state = StatePendingCutoff
						n.execAt = n.deferCutoff(t)
						n.log(Event{Kind: EvCutoffScheduled, Time: t, ExecTime: n.execAt, Balance: n.balance, Arrears: n.arrears})
						n.advance(n.clock)
					}
				case StatePendingRestore:
					if n.balance < n.cfg.RestoreThreshold {
						n.state = StateCutOff
						n.since = t
						n.log(Event{Kind: EvRestoreCancelled, Time: t, Balance: n.balance, Arrears: n.arrears})
					}
				}
			}
		}
	}
	n.hasRead = true
	n.lastT = t
	n.lastE = energy
	return nil
}

func (n *naiveAccount) recharge(t, amount int64) error {
	if t < 0 || amount <= 0 {
		return newErr(ErrInvalidParam)
	}
	if t < n.clock {
		return newErr(ErrClockRegression)
	}
	n.advance(t)
	repay := amount
	if repay > n.emUsed {
		repay = n.emUsed
	}
	n.emUsed -= repay
	n.totP += repay
	rem := amount - repay
	pay := rem * n.cfg.ArrearsRatioNum / n.cfg.ArrearsRatioDen
	if pay > n.arrears {
		pay = n.arrears
	}
	n.arrears -= pay
	n.balance += rem - pay
	n.totR += amount
	n.log(Event{Kind: EvRecharge, Time: t, Amount: amount, RepayEmergency: repay, RepayArrears: pay, ToBalance: rem - pay, Balance: n.balance, Arrears: n.arrears})
	n.warnCheck(t)
	switch n.state {
	case StatePendingCutoff:
		if n.balance >= 0 {
			n.state = StateSupplyOn
			n.log(Event{Kind: EvCutoffCancelled, Time: t, Balance: n.balance, Arrears: n.arrears})
		}
	case StateCutOff:
		if n.balance >= n.cfg.RestoreThreshold {
			n.pCutDur += t - n.since
			n.state = StatePendingRestore
			n.deadline = t + n.cfg.ConfirmTimeout
			n.log(Event{Kind: EvPendingRestore, Time: t, ExecTime: n.deadline, Balance: n.balance, Arrears: n.arrears})
		}
	}
	return nil
}

func (n *naiveAccount) enableEmergency(t int64) error {
	if t < 0 {
		return newErr(ErrInvalidParam)
	}
	if t < n.clock {
		return newErr(ErrClockRegression)
	}
	eff := n.state
	if eff == StatePendingCutoff && n.execAt <= t {
		eff = StateCutOff
	}
	if eff == StatePendingRestore && n.deadline < t {
		eff = StateCutOff
	}
	if eff == StateCutOff || eff == StatePendingRestore {
		return newErr(ErrStateNotAllowed)
	}
	if n.emPeriod == t/n.cfg.PeriodLength {
		return newErr(ErrAlreadyEnabled)
	}
	if n.balance >= n.cfg.EmergencyThreshold {
		return newErr(ErrConditionNotMet)
	}
	n.advance(t)
	n.balance += n.cfg.EmergencyAmount
	n.emUsed += n.cfg.EmergencyAmount
	n.totG += n.cfg.EmergencyAmount
	n.emPeriod = t / n.cfg.PeriodLength
	n.log(Event{Kind: EvEmergencyEnabled, Time: t, Amount: n.cfg.EmergencyAmount, Balance: n.balance, Arrears: n.arrears})
	n.warnCheck(t)
	if n.state == StatePendingCutoff {
		if n.balance >= 0 {
			n.state = StateSupplyOn
			n.log(Event{Kind: EvCutoffCancelled, Time: t, Balance: n.balance, Arrears: n.arrears})
		} else {
			n.execAt = n.deferCutoff(t)
			n.log(Event{Kind: EvCutoffRescheduled, Time: t, ExecTime: n.execAt, Balance: n.balance, Arrears: n.arrears})
			n.advance(n.clock)
		}
	}
	return nil
}

func (n *naiveAccount) confirmRestore(t int64) error {
	if t < 0 {
		return newErr(ErrInvalidParam)
	}
	if t < n.clock {
		return newErr(ErrClockRegression)
	}
	if n.state != StatePendingRestore {
		return newErr(ErrStateNotAllowed)
	}
	if t > n.deadline {
		return newErr(ErrConfirmTimeout)
	}
	n.advance(t)
	n.state = StateSupplyOn
	n.log(Event{Kind: EvRestored, Time: t, Balance: n.balance, Arrears: n.arrears})
	return nil
}

func (n *naiveAccount) advanceClock(t int64) error {
	if t < 0 {
		return newErr(ErrInvalidParam)
	}
	if t < n.clock {
		return newErr(ErrClockRegression)
	}
	n.advance(t)
	return nil
}

func (n *naiveAccount) setPrice(t, priceMilli int64) error {
	if t < 0 || priceMilli <= 0 {
		return newErr(ErrInvalidParam)
	}
	if t < n.clock {
		return newErr(ErrClockRegression)
	}
	if t <= n.priceT[len(n.priceT)-1] {
		return newErr(ErrTimeOrder)
	}
	n.advance(t)
	n.priceT = append(n.priceT, t)
	n.priceV = append(n.priceV, priceMilli)
	n.log(Event{Kind: EvPriceSet, Time: t, Price: priceMilli, Balance: n.balance, Arrears: n.arrears})
	return nil
}

func (n *naiveAccount) setWarnThreshold(v int64) error {
	if v <= 0 {
		return newErr(ErrInvalidParam)
	}
	n.cfg.WarnThreshold = v
	return nil
}

func (n *naiveAccount) addHoliday(day int64) error {
	if day < 0 {
		return newErr(ErrInvalidParam)
	}
	n.holidays = append(n.holidays, day)
	return nil
}

func (n *naiveAccount) snapshot() Snapshot {
	return Snapshot{
		Clock:                 n.clock,
		Balance:               n.balance,
		Arrears:               n.arrears,
		EmergencyUsed:         n.emUsed,
		State:                 n.state,
		Warned:                n.warned,
		CutoffExecAt:          n.execAt,
		RestoreDeadline:       n.deadline,
		TotalRecharge:         n.totR,
		TotalEmergencyGranted: n.totG,
		TotalCharged:          n.totC,
		TotalEmergencyRepaid:  n.totP,
	}
}

func (n *naiveAccount) invariant() bool {
	return n.totR+n.totG == n.totC+n.balance+n.totP-n.arrears
}

// ---- 随机操作序列对照 ----

type opKind int

const (
	opReading opKind = iota
	opRecharge
	opEnable
	opConfirm
	opAdvance
	opPrice
	opWarnThr
	opHoliday
)

type op struct {
	kind opKind
	a, b int64
}

func (o op) String() string {
	name := []string{"读数", "充值", "应急启用", "复电确认", "推进时钟", "电价变更", "预警阈值", "节假日"}[o.kind]
	return fmt.Sprintf("%s(%d,%d)", name, o.a, o.b)
}

func applyReal(a *Account, o op) error {
	switch o.kind {
	case opReading:
		return a.AddReading(o.a, o.b)
	case opRecharge:
		return a.Recharge(o.a, o.b)
	case opEnable:
		return a.EnableEmergency(o.a)
	case opConfirm:
		return a.ConfirmRestore(o.a)
	case opAdvance:
		return a.AdvanceClock(o.a)
	case opPrice:
		return a.SetPrice(o.a, o.b)
	case opWarnThr:
		return a.SetWarnThreshold(o.a)
	case opHoliday:
		return a.AddHoliday(o.a)
	}
	panic("bad op")
}

func applyNaive(n *naiveAccount, o op) error {
	switch o.kind {
	case opReading:
		return n.addReading(o.a, o.b)
	case opRecharge:
		return n.recharge(o.a, o.b)
	case opEnable:
		return n.enableEmergency(o.a)
	case opConfirm:
		return n.confirmRestore(o.a)
	case opAdvance:
		return n.advanceClock(o.a)
	case opPrice:
		return n.setPrice(o.a, o.b)
	case opWarnThr:
		return n.setWarnThreshold(o.a)
	case opHoliday:
		return n.addHoliday(o.a)
	}
	panic("bad op")
}

func genOp(rng *rand.Rand, now, lastE, lastRT int64, hasRead bool) op {
	switch r := rng.Intn(100); {
	case r < 35: // 读数，偶发时钟回退/时序错误/读数倒退
		t := now + rng.Int63n(1500)
		e := lastE + rng.Int63n(60)
		switch rng.Intn(20) {
		case 0:
			t = now - rng.Int63n(10) - 1
		case 1:
			if hasRead {
				t = lastRT
			}
		case 2:
			if hasRead {
				e = lastE - rng.Int63n(5) - 1
			}
		}
		return op{opReading, t, e}
	case r < 55: // 充值，偶发非正金额
		amount := rng.Int63n(300)
		if rng.Intn(15) == 0 {
			amount = -rng.Int63n(10)
		}
		return op{opRecharge, now + rng.Int63n(200), amount}
	case r < 63:
		return op{opEnable, now + rng.Int63n(100), 0}
	case r < 68:
		return op{opConfirm, now + rng.Int63n(120), 0}
	case r < 78:
		return op{opAdvance, now + rng.Int63n(3000), 0}
	case r < 83:
		return op{opPrice, now + rng.Int63n(100), 500 + rng.Int63n(3000)}
	case r < 88:
		return op{opWarnThr, rng.Int63n(400) - 50, 0}
	case r < 93:
		return op{opHoliday, now/secondsPerDay + rng.Int63n(4) - 1, 0}
	default:
		return op{opReading, now + rng.Int63n(800), lastE + rng.Int63n(40)}
	}
}

func diffCfg() Config {
	return Config{
		InitialPriceMilli:  1500,
		WarnThreshold:      120,
		RestoreThreshold:   30,
		EmergencyAmount:    80,
		EmergencyThreshold: 20,
		ArrearsRatioNum:    3,
		ArrearsRatioDen:    5,
		ConfirmTimeout:     50,
		PeriodLength:       500,
		FriendlyStartSec:   70000, // 跨午夜友好时段
		FriendlyEndSec:     20000,
		RestWeekdays:       [7]bool{6: true},
		EpochWeekday:       3,
	}
}

// 与独立朴素模型对照大量随机操作序列：
// 逐条比对错误类别、事件流与状态快照，并逐步校验资金守恒不变量。
// 日志打印每条输入、输出与判定依据（go test -v 可见）。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		rng := rand.New(rand.NewSource(seed))
		real, err := NewAccount(diffCfg())
		if err != nil {
			t.Fatal(err)
		}
		naive := newNaive(diffCfg())
		var lastE, lastRT int64
		hasRead := false
		evCount := 0
		for i := 0; i < 400; i++ {
			now := real.Snapshot().Clock
			o := genOp(rng, now, lastE, lastRT, hasRead)
			pre := real.Snapshot()
			errR := applyReal(real, o)
			errN := applyNaive(naive, o)
			if CodeOf(errR) != CodeOf(errN) {
				t.Fatalf("seed=%d op#%d %s: error mismatch real=%v naive=%v", seed, i, o, errR, errN)
			}
			if errR == nil && o.kind == opReading {
				lastE, lastRT, hasRead = o.b, o.a, true
			}
			evs := real.Events()
			if !slices.Equal(evs, naive.events) {
				t.Fatalf("seed=%d op#%d %s: event mismatch\nreal : %v\nnaive: %v", seed, i, o, evs[evCount:], naive.events[evCount:])
			}
			post := real.Snapshot()
			if ns := naive.snapshot(); post != ns {
				t.Fatalf("seed=%d op#%d %s: state mismatch\nreal : %+v\nnaive: %+v", seed, i, o, post, ns)
			}
			if !real.CheckInvariant() || !naive.invariant() {
				t.Fatalf("seed=%d op#%d %s: invariant violated", seed, i, o)
			}
			t.Logf("seed=%d op#%d in=%s 依据{clk=%d bal=%d arr=%d st=%s emU=%d warned=%v} -> out{err=%v bal=%d arr=%d st=%s} events=%v",
				seed, i, o, pre.Clock, pre.Balance, pre.Arrears, pre.State, pre.EmergencyUsed, pre.Warned,
				errR, post.Balance, post.Arrears, post.State, evs[evCount:])
			evCount = len(evs)
		}
	}
}

// 相同操作序列重放得到完全相同的事件序列。
func TestReplayDeterminism(t *testing.T) {
	run := func(seed int64) []Event {
		rng := rand.New(rand.NewSource(seed))
		a, err := NewAccount(diffCfg())
		if err != nil {
			t.Fatal(err)
		}
		var lastE, lastRT int64
		hasRead := false
		for i := 0; i < 400; i++ {
			o := genOp(rng, a.Snapshot().Clock, lastE, lastRT, hasRead)
			if applyReal(a, o) == nil && o.kind == opReading {
				lastE, lastRT, hasRead = o.b, o.a, true
			}
		}
		return a.Events()
	}
	if !slices.Equal(run(42), run(42)) {
		t.Fatal("replay produced different event sequences")
	}
}

// 并发调用等价于某个串行执行顺序：
// 竞态检测器验证内存安全，结束后资金守恒不变量必须成立。
func TestConcurrentSerializable(t *testing.T) {
	a, err := NewAccount(diffCfg())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			var energy int64
			for i := 0; i < 300; i++ {
				ts := int64(g*100000 + i*10)
				switch rng.Intn(6) {
				case 0:
					energy += rng.Int63n(20)
					_ = a.AddReading(ts, energy)
				case 1:
					_ = a.Recharge(ts, 1+rng.Int63n(200))
				case 2:
					_ = a.EnableEmergency(ts)
				case 3:
					_ = a.ConfirmRestore(ts)
				case 4:
					_ = a.AdvanceClock(ts)
				case 5:
					_ = a.SetPrice(ts, 500+rng.Int63n(3000))
				}
			}
		}(g)
	}
	wg.Wait()
	if !a.CheckInvariant() {
		t.Fatal("invariant violated after concurrent ops")
	}
}
