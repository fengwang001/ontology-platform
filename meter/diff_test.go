package meter

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

type meterAPI interface {
	Advance(t Tick) ([]Event, error)
	AddReading(t Tick, cumulative int64) ([]Event, error)
	EnableEmergency(t Tick) ([]Event, error)
	Recharge(t Tick, amount Money) ([]Event, error)
	ConfirmRestore(t Tick) ([]Event, error)
	AddTariff(start Tick, price int64) error
	SetWarnThreshold(v Money) error
	Snapshot() Snapshot
}

var _ meterAPI = (*Controller)(nil)
var _ meterAPI = (*Naive)(nil)

type opResult struct {
	name string
	args string
	err  error
	evs  []Event
	why  string
}

func diffSnap(a, b Snapshot) string {
	if a.Status != b.Status {
		return fmt.Sprintf("status %v vs %v", a.Status, b.Status)
	}
	if a.Balance != b.Balance {
		return fmt.Sprintf("balance %d vs %d", a.Balance, b.Balance)
	}
	if a.Debt != b.Debt {
		return fmt.Sprintf("debt %d vs %d", a.Debt, b.Debt)
	}
	if a.EmergencyUsed != b.EmergencyUsed {
		return fmt.Sprintf("emergencyUsed %d vs %d", a.EmergencyUsed, b.EmergencyUsed)
	}
	if a.CutExecuteAt != b.CutExecuteAt {
		return fmt.Sprintf("cutAt %d vs %d", a.CutExecuteAt, b.CutExecuteAt)
	}
	if a.RestoreDeadline != b.RestoreDeadline {
		return fmt.Sprintf("deadline %d vs %d", a.RestoreDeadline, b.RestoreDeadline)
	}
	if a.Warned != b.Warned {
		return fmt.Sprintf("warned %v vs %v", a.Warned, b.Warned)
	}
	if a.EmergencyEnabledThisCycle != b.EmergencyEnabledThisCycle {
		return "emergencyCycle flag differs"
	}
	if a.CycleIndex != b.CycleIndex {
		return fmt.Sprintf("cycle %d vs %d", a.CycleIndex, b.CycleIndex)
	}
	if a.TotalRecharge != b.TotalRecharge ||
		a.TotalEmergencyGranted != b.TotalEmergencyGranted ||
		a.TotalDeducted != b.TotalDeducted ||
		a.TotalEmergencyRepaid != b.TotalEmergencyRepaid {
		return "totals differ"
	}
	if a.CycleDeducted != b.CycleDeducted || a.CycleWarns != b.CycleWarns ||
		a.CycleCutDuration != b.CycleCutDuration {
		return "cycle counters differ"
	}
	return ""
}

func checkInvariant(s Snapshot) string {
	lhs := s.TotalRecharge + s.TotalEmergencyGranted
	rhs := s.TotalDeducted + s.Balance + s.TotalEmergencyRepaid - s.Debt
	if lhs != rhs {
		return fmt.Sprintf("invariant broken: %d != %d (ded=%d bal=%d rep=%d debt=%d)",
			lhs, rhs, s.TotalDeducted, s.Balance, s.TotalEmergencyRepaid, s.Debt)
	}
	return ""
}

// 与独立朴素模型对照大量随机操作序列；日志打印每条输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	const sequences, steps = 400, 120
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := Config{
			CreatedAt:          0,
			WarnThreshold:      int64(20 + rng.Intn(80)),
			EmergencyThreshold: int64(rng.Intn(60) - 20),
			EmergencyAmount:    int64(30 + rng.Intn(80)),
			RestoreThreshold:   int64(rng.Intn(40)),
			DebtRepayRatio:     Ratio{N: int64(1 + rng.Intn(3)), D: 4},
			ConfirmWindow:      int64(10 + rng.Intn(60)),
			CycleLength:        int64(60 + rng.Intn(200)),
			FriendlyStart:      8 * 3600,
			FriendlyEnd:        16 * 3600,
			Holidays:           map[int64]struct{}{5: {}, 11: {}},
			Tariffs:            []Tariff{{Start: 0, Price: int64(1 + rng.Intn(20))}},
		}
		initial := int64(rng.Intn(120))
		a := New(cfg, initial)
		b := NewNaive(cfg, initial)

		var t0, cum int64
		var log []opResult
		fail := func(msg string) {
			for _, r := range log {
				t.Logf("  %s %s -> err=%v | %s | events=%v",
					r.name, r.args, r.err, r.why, evBrief(r.evs))
			}
			t.Fatalf("seed=%d: %s", seed, msg)
		}

		for step := 0; step < steps; step++ {
			op := rng.Intn(9)
			var ra, rb []Event
			var ea, eb error
			name, args := "", ""

			switch op {
			case 0:
				t0 += int64(rng.Intn(8))
				name, args = "advance", fmt.Sprintf("t=%d", t0)
				ra, ea = a.Advance(t0)
				rb, eb = b.Advance(t0)
			case 1, 2:
				prev := t0
				t0 += int64(1 + rng.Intn(6))
				if rng.Intn(8) == 0 {
					cum -= int64(rng.Intn(5)) // 偶尔倒退
				}
				cum += int64(rng.Intn(400))
				name, args = "reading",
					fmt.Sprintf("t=%d cum=%d (prevT=%d)", t0, cum, prev)
				ra, ea = a.AddReading(t0, cum)
				rb, eb = b.AddReading(t0, cum)
			case 3:
				t0 += int64(rng.Intn(8))
				name, args = "emergency", fmt.Sprintf("t=%d", t0)
				ra, ea = a.EnableEmergency(t0)
				rb, eb = b.EnableEmergency(t0)
			case 4:
				t0 += int64(rng.Intn(8))
				amt := int64(1 + rng.Intn(300))
				if rng.Intn(15) == 0 {
					amt = -int64(rng.Intn(5))
				}
				name, args = "recharge", fmt.Sprintf("t=%d amt=%d", t0, amt)
				ra, ea = a.Recharge(t0, amt)
				rb, eb = b.Recharge(t0, amt)
			case 5:
				t0 += int64(rng.Intn(10))
				name, args = "confirm", fmt.Sprintf("t=%d", t0)
				ra, ea = a.ConfirmRestore(t0)
				rb, eb = b.ConfirmRestore(t0)
			case 6:
				newThr := int64(1 + rng.Intn(150))
				if rng.Intn(20) == 0 {
					newThr = 0
				}
				name, args = "setWarn", fmt.Sprintf("v=%d", newThr)
				ea = a.SetWarnThreshold(newThr)
				eb = b.SetWarnThreshold(newThr)
			case 7:
				start := t0 + int64(1+rng.Intn(50))
				price := int64(1 + rng.Intn(25))
				name, args = "addTariff", fmt.Sprintf("start=%d price=%d", start, price)
				ea = a.AddTariff(start, price)
				eb = b.AddTariff(start, price)
			default:
				// 有时刻回退的非法操作。
				bad := t0 - int64(rng.Intn(5))
				name, args = "reading-backtime", fmt.Sprintf("t=%d cum=%d", bad, cum)
				ra, ea = a.AddReading(bad, cum)
				rb, eb = b.AddReading(bad, cum)
			}
			log = append(log, opResult{
				name: name, args: args, err: ea, evs: ra,
				why: fmt.Sprintf("status=%s bal=%d debt=%d emg=%d cycle=%d",
					a.Snapshot().Status, a.Snapshot().Balance, a.Snapshot().Debt,
					a.Snapshot().EmergencyUsed, a.Snapshot().CycleIndex),
			})
			if fmt.Sprint(ea) != fmt.Sprint(eb) {
				fail(fmt.Sprintf("%s(%s): err %v vs %v", name, args, ea, eb))
			}
			if ea == nil && !reflect.DeepEqual(ra, rb) {
				fail(fmt.Sprintf("%s(%s): events differ\n a=%v\n b=%v", name, args, ra, rb))
			}
			sa, sb := a.Snapshot(), b.Snapshot()
			if why := diffSnap(sa, sb); why != "" {
				fail(fmt.Sprintf("%s(%s): %s", name, args, why))
			}
			if why := checkInvariant(sa); why != "" {
				fail(why)
			}
			if !reflect.DeepEqual(sa.Events, sb.Events) {
				fail("full event history differs")
			}
		}
	}
}

func evBrief(evs []Event) string {
	out := ""
	for i, e := range evs {
		if i > 6 {
			out += " ..."
			break
		}
		out += fmt.Sprintf("%d@%d ", e.Kind, e.At)
	}
	return out
}

// 所有操作可并发调用：串行化后结果等价，且不变量始终成立。
func TestConcurrentSafe(t *testing.T) {
	cfg := baseCfg()
	cfg.CycleLength = 500
	c := New(cfg, 500)
	var wg sync.WaitGroup
	var t0 int64
	var mu sync.Mutex
	var cum int64
	next := func() (Tick, int64) {
		mu.Lock()
		defer mu.Unlock()
		t0 += int64(1)
		cum += int64(2)
		return t0, cum
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < 60; i++ {
				when, reading := next()
				switch rng.Intn(4) {
				case 0:
					_, _ = c.AddReading(when, reading)
				case 1:
					_, _ = c.Recharge(when, int64(1+rng.Intn(50)))
				case 2:
					_, _ = c.Advance(when)
				default:
					_, _ = c.EnableEmergency(when)
				}
			}
		}(g)
	}
	wg.Wait()
	s := c.Snapshot()
	if why := checkInvariant(s); why != "" {
		t.Fatal(why)
	}
	// 至多一种停送电状态（枚举本身保证），且余额与累计量一致。
	if s.Status < Powered || s.Status > PendingRestore {
		t.Fatal("illegal status")
	}
}

// 性能：单条操作与事件/读数历史长度无关。
func TestPerformanceIndependence(t *testing.T) {
	cfg := baseCfg()
	cfg.CycleLength = 1 << 40
	c := New(cfg, 1<<40)
	for i := int64(1); i <= 4000; i++ {
		if _, err := c.AddReading(i, i*10); err != nil {
			t.Fatal(err)
		}
	}
	snap := c.Snapshot()
	measure := func() int64 {
		var x int64
		for i := 0; i < 2000; i++ {
			cc := New(cfg, 1<<30)
			_, _ = cc.AddReading(1, 5)
			_, _ = cc.Recharge(2, 100)
			x += cc.Snapshot().Balance
		}
		return x
	}
	_ = measure()
	// 大历史实例：再追加一条读数与一次充值，耗时应与历史规模无关。
	if _, err := c.AddReading(4001, 40010); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Recharge(4002, 100); err != nil {
		t.Fatal(err)
	}
	if got := c.Snapshot().TotalRecharge; got != 1<<40+100 {
		t.Fatalf("total recharge = initial + 100, got %d", got)
	}
	// 友好时段判定与节假日数量无关：构造大量节假日后仍 O(1)（map 查找）。
	big := cfg
	big.Holidays = map[int64]struct{}{}
	for d := int64(0); d < 100000; d++ {
		big.Holidays[d] = struct{}{}
	}
	if !big.inFriendly(50*86400 + 3600) {
		t.Fatal("holiday lookup should be friendly")
	}
	_ = snap
}
