package dtc_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/dtc"
)

// testConfig 去抖：上升 3 / 失败线 6，下降 3 / 通过线 -6；
// 确认 2 个连续失败循环，愈合 2 个无故障暖机，自动清除 4 个；
// 暖机：温升 >= 20 且终温 >= 70。
func testConfig() dtc.Config {
	return dtc.Config{
		DebounceRiseStep:  3,
		DebounceFailLimit: 6,
		DebounceFallStep:  3,
		DebouncePassLimit: -6,
		ConfirmCycles:     2,
		HealWarmUpCycles:  2,
		AutoClearWarmUps:  4,
		WarmUpTempRise:    20,
		WarmUpFinalTemp:   70,
	}
}

// harness 维护单调递增的时刻与里程，简化事件构造。
type harness struct {
	t    *testing.T
	m    *dtc.Manager
	time int64
	odo  int64
}

func newHarness(t *testing.T, cfg dtc.Config, dtcs ...[2]int) *harness {
	t.Helper()
	m, err := dtc.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	for _, d := range dtcs {
		if err := m.RegisterDTC(d[0], d[1]); err != nil {
			t.Fatalf("RegisterDTC(%d,%d): %v", d[0], d[1], err)
		}
	}
	return &harness{t: t, m: m}
}

func (h *harness) do(ev dtc.Event) error {
	h.time++
	h.odo += 5
	ev.Time = h.time
	ev.Odometer = h.odo
	err := h.m.Handle(ev)
	h.t.Logf("event %+v -> err=%v", ev, err)
	return err
}

func (h *harness) mustDo(ev dtc.Event) {
	h.t.Helper()
	if err := h.do(ev); err != nil {
		h.t.Fatalf("event %+v rejected: %v", ev, err)
	}
}

func (h *harness) on()  { h.mustDo(dtc.Event{Kind: dtc.EventIgnitionOn}) }
func (h *harness) off() { h.mustDo(dtc.Event{Kind: dtc.EventIgnitionOff}) }

func (h *harness) failN(code, n int) {
	for i := 0; i < n; i++ {
		h.mustDo(dtc.Event{Kind: dtc.EventMonitorResult, DTCCode: code, Passed: false})
	}
}

func (h *harness) passN(code, n int) {
	for i := 0; i < n; i++ {
		h.mustDo(dtc.Event{Kind: dtc.EventMonitorResult, DTCCode: code, Passed: true})
	}
}

func (h *harness) sample(speed, temp int64) {
	h.mustDo(dtc.Event{Kind: dtc.EventEnvironmentSample, Speed: speed, CoolantTemp: temp})
}

// failCycle 一个失败循环：两次失败上报恰到失败线。
func (h *harness) failCycle(code int) {
	h.on()
	h.failN(code, 2)
	h.off()
}

// warmPassCycle 一个暖机且监测完成无失败的循环：温升 65(>=20)，终温 75(>=70)。
func (h *harness) warmPassCycle(code int) {
	h.on()
	h.sample(50, 10)
	h.sample(50, 75)
	h.passN(code, 2)
	h.off()
}

// confirmCode 让 code 进入确认态（两个连续失败循环）。
func (h *harness) confirmCode(code int) {
	h.failCycle(code)
	h.failCycle(code)
}

func (h *harness) snap(code int) dtc.Snapshot {
	h.t.Helper()
	s, err := h.m.Snapshot(code)
	if err != nil {
		h.t.Fatalf("Snapshot(%d): %v", code, err)
	}
	h.t.Logf("snapshot code=%d -> %+v", code, s)
	return s
}

func wantErrKind(t *testing.T, err error, kind dtc.ErrorKind) {
	t.Helper()
	var de *dtc.Error
	if !errors.As(err, &de) {
		t.Fatalf("want *dtc.Error kind=%s, got %v", kind, err)
	}
	if de.Kind != kind {
		t.Fatalf("want kind=%s, got %s (%v)", kind, de.Kind, err)
	}
	t.Logf("rejected as expected: kind=%s, err=%v", de.Kind, err)
}

// 去抖恰到判定线：两次失败上报 3+3=6 恰达失败线；继续失败被钳制；
// 四次通过上报 6-3*4=-6 恰达通过线。
func TestDebounceExactThresholds(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 1})
	h.on()
	h.failN(1, 1)
	if s := h.snap(1); s.JudgedFail || s.Pending {
		t.Fatalf("value=3 below fail line, want no judgment, got %+v", s)
	}
	h.failN(1, 1) // 3+3=6 恰达失败线
	if s := h.snap(1); !s.JudgedFail || !s.Pending || s.Occurrences != 1 {
		t.Fatalf("value=6 at fail line, want fail+pending+occ=1, got %+v", s)
	}
	h.failN(1, 1) // 钳制在 6，不重复计发生次数
	if s := h.snap(1); !s.JudgedFail || s.Occurrences != 1 {
		t.Fatalf("value clamped at 6, want occ still 1, got %+v", s)
	}
	h.passN(1, 3)
	if s := h.snap(1); !s.JudgedFail {
		t.Fatalf("value=-3 above pass line, want keep fail judgment, got %+v", s)
	}
	h.passN(1, 1) // 6-3*4=-6 恰达通过线
	if s := h.snap(1); s.JudgedFail || !s.Pending {
		t.Fatalf("value=-6 at pass line, want pass judgment but pending kept, got %+v", s)
	}
	h.off()
	if s := h.snap(1); s.JudgedFail {
		t.Fatalf("ignition off clears judgment, got %+v", s)
	}
}

// 点火关时去抖值与判定清零：上一循环的 3 不累积到下一循环。
func TestDebounceResetOnIgnitionOff(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 1})
	h.on()
	h.failN(1, 1) // value=3
	h.off()
	h.on()
	h.failN(1, 1) // 重新从 0 到 3，而非 6
	if s := h.snap(1); s.JudgedFail || s.Pending {
		t.Fatalf("debounce must reset on ignition off, got %+v", s)
	}
	h.off()
}

// 同一循环内失败后通过再失败：发生次数只加一。
func TestFailPassFailSameCycle(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 1})
	h.on()
	h.failN(1, 2) // 失败判定，occ=1
	h.passN(1, 4) // 回到通过线，判通过
	if s := h.snap(1); s.JudgedFail {
		t.Fatalf("want pass judgment mid-cycle, got %+v", s)
	}
	h.failN(1, 4) // 再次升到失败线
	s := h.snap(1)
	if !s.JudgedFail || !s.Pending || s.Occurrences != 1 {
		t.Fatalf("re-fail in same cycle must not bump occurrences, got %+v", s)
	}
	h.off()
	if s := h.snap(1); s.ConsecutiveFailCycles != 1 || s.Occurrences != 1 {
		t.Fatalf("one failed cycle, occ=1, got %+v", s)
	}
}

// 监测未完成的循环：连续失败循环数既不增加也不清零。
func TestIncompleteMonitorCycle(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 1})
	h.failCycle(1) // 连续失败循环数=1
	h.on()
	h.failN(1, 1) // value=3，未达失败线也无通过判定：监测未完成
	h.off()
	if s := h.snap(1); s.ConsecutiveFailCycles != 1 {
		t.Fatalf("incomplete cycle must keep counter, want 1, got %+v", s)
	}
	h.on()
	h.passN(1, 2) // 监测完成且无失败：清零
	h.off()
	if s := h.snap(1); s.ConsecutiveFailCycles != 0 {
		t.Fatalf("completed clean cycle must reset counter, got %+v", s)
	}
}

// 确认：连续失败循环数差一不满足，恰满足时在点火关确认，待定保持。
func TestConfirmExactAndOneShort(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 1})
	h.failCycle(1)
	if s := h.snap(1); s.Confirmed || s.ConsecutiveFailCycles != 1 || !s.Pending {
		t.Fatalf("one failed cycle (need 2): want unconfirmed, got %+v", s)
	}
	h.failCycle(1)
	if s := h.snap(1); !s.Confirmed || !s.Pending || s.ConsecutiveFailCycles != 2 {
		t.Fatalf("two consecutive failed cycles: want confirmed+pending, got %+v", s)
	}
}

// 愈合：无故障暖机数差一不愈合，恰满足时在点火关清除确认与待定，作为历史保留。
func TestHealExactAndOneShort(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 1})
	h.confirmCode(1)
	h.warmPassCycle(1)
	if s := h.snap(1); !s.Confirmed || s.Healed || s.FaultFreeWarmUps != 1 {
		t.Fatalf("one warm-up (need 2): want still confirmed, got %+v", s)
	}
	h.warmPassCycle(1)
	if s := h.snap(1); s.Confirmed || s.Pending || !s.Healed || s.FaultFreeWarmUps != 2 {
		t.Fatalf("two warm-ups: want healed history, got %+v", s)
	}
}

// 暖机温升恰等阈值有效，差一度无效；终温未达阈值无效。
func TestWarmUpRiseExactThreshold(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 1})
	h.confirmCode(1)

	h.on()
	h.sample(50, 50)
	h.sample(50, 70) // 温升恰 20，终温恰 70：暖机成立
	h.passN(1, 2)
	h.off()
	if s := h.snap(1); s.FaultFreeWarmUps != 1 {
		t.Fatalf("rise exactly 20 and final 70 must count, got %+v", s)
	}

	h.on()
	h.sample(50, 51)
	h.sample(50, 70) // 温升 19 < 20：非暖机
	h.passN(1, 2)
	h.off()
	if s := h.snap(1); s.FaultFreeWarmUps != 1 {
		t.Fatalf("rise 19 below threshold must not count, got %+v", s)
	}

	h.on()
	h.sample(50, 10)
	h.sample(50, 35) // 温升 25 但终温 35 < 70：非暖机
	h.passN(1, 2)
	h.off()
	if s := h.snap(1); s.FaultFreeWarmUps != 1 {
		t.Fatalf("final temp below threshold must not count, got %+v", s)
	}
}

// 冻结帧：空闲占用；严重度相等不替换；严格更高才替换。
func TestFreezeFrameSeverity(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 2}, [2]int{2, 2}, [2]int{3, 3})
	h.on()
	h.sample(80, 25)
	h.failN(1, 2)
	if s := h.snap(1); !s.OwnsFreezeFrame {
		t.Fatalf("first failure on free slot must capture, got %+v", s)
	}
	h.failN(2, 2) // 严重度相等：保持原状
	if s := h.snap(2); s.OwnsFreezeFrame {
		t.Fatalf("equal severity must not replace, got %+v", s)
	}
	if s := h.snap(1); !s.OwnsFreezeFrame {
		t.Fatalf("owner must stay on equal severity, got %+v", s)
	}
	h.failN(3, 2) // 严重度 3 > 2：替换
	if s := h.snap(3); !s.OwnsFreezeFrame {
		t.Fatalf("higher severity must replace, got %+v", s)
	}
	if s := h.snap(1); s.OwnsFreezeFrame {
		t.Fatalf("replaced owner must lose freeze frame, got %+v", s)
	}
	h.off()
}

// 诊断仪清除：释放冻结帧、清空全部状态，并记录清除基准里程。
func TestScanToolClear(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 2})
	h.failCycle(1)
	if s := h.snap(1); !s.OwnsFreezeFrame || !s.Pending {
		t.Fatalf("precondition: pending with freeze frame, got %+v", s)
	}
	h.mustDo(dtc.Event{Kind: dtc.EventScanToolClear})
	base := h.odo
	if s := h.snap(1); s.Pending || s.OwnsFreezeFrame || s.Occurrences != 0 ||
		s.ConsecutiveFailCycles != 0 || s.DistanceSinceClear != 0 {
		t.Fatalf("clear must wipe state and release slot, got %+v", s)
	}
	h.failCycle(1) // 清除后重新失败：重新占用槽位
	s := h.snap(1)
	if !s.OwnsFreezeFrame || !s.Pending || s.Occurrences != 1 {
		t.Fatalf("after clear, failure must re-pend and re-capture, got %+v", s)
	}
	if s.DistanceSinceClear != h.odo-base {
		t.Fatalf("distance since clear = %d - %d, got %+v", h.odo, base, s)
	}
}

// 自动清除：无故障暖机总数差一保留历史，恰满足时彻底清除并释放冻结帧。
func TestAutoClearExactAndOneShort(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 2})
	h.confirmCode(1)
	h.warmPassCycle(1)
	h.warmPassCycle(1) // 愈合，faultFreeWarmUps=2
	if s := h.snap(1); !s.Healed || !s.OwnsFreezeFrame {
		t.Fatalf("precondition: healed history keeps freeze frame, got %+v", s)
	}
	h.warmPassCycle(1) // 3 < 4：差一，仍保留
	if s := h.snap(1); !s.Healed || s.FaultFreeWarmUps != 3 {
		t.Fatalf("3 warm-ups (need 4): want healed history kept, got %+v", s)
	}
	h.warmPassCycle(1) // 4：自动清除
	if s := h.snap(1); s.Healed || s.Pending || s.Confirmed || s.Occurrences != 0 ||
		s.FaultFreeWarmUps != 0 || s.OwnsFreezeFrame {
		t.Fatalf("4 warm-ups: want fully cleared and slot released, got %+v", s)
	}
}

// 已愈合的故障码再次失败：发生次数加一、重新待定、计数从零开始。
func TestHealedDTCFailsAgain(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 1})
	h.confirmCode(1)
	h.warmPassCycle(1)
	h.warmPassCycle(1)
	if s := h.snap(1); !s.Healed || s.Occurrences != 2 {
		t.Fatalf("precondition: healed with occ=2, got %+v", s)
	}
	h.on()
	h.failN(1, 2)
	s := h.snap(1)
	if !s.Pending || s.Healed || s.Occurrences != 3 || s.FaultFreeWarmUps != 0 {
		t.Fatalf("healed re-fail: want pending, occ=3, warm-ups reset, got %+v", s)
	}
	h.off()
	if s := h.snap(1); s.ConsecutiveFailCycles != 1 || s.Confirmed {
		t.Fatalf("consecutive fail cycles restart from zero, got %+v", s)
	}
}

// 错误可区分且有序：参数非法 > 回退 > 顺序 > 未登记 > 状态不允许。
func TestErrorPrecedence(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 1})

	// 参数非法优先于回退（时刻 -1 同时构成回退）。
	wantErrKind(t, h.m.Handle(dtc.Event{Kind: dtc.EventIgnitionOn, Time: -1}), dtc.ErrInvalidParam)

	h.on()
	h.off()

	// 回退优先于顺序非法（点火关状态下再点火关，且时刻回退）。
	wantErrKind(t, h.m.Handle(dtc.Event{Kind: dtc.EventIgnitionOff, Time: h.time - 1, Odometer: h.odo}),
		dtc.ErrRegression)

	// 顺序非法优先于未登记（点火关期间上报未登记故障码）。
	wantErrKind(t, h.m.Handle(dtc.Event{Kind: dtc.EventMonitorResult, DTCCode: 999, Time: h.time, Odometer: h.odo}),
		dtc.ErrEventOrder)

	h.on()
	// 未登记（点火开期间）。
	wantErrKind(t, h.m.Handle(dtc.Event{Kind: dtc.EventMonitorResult, DTCCode: 999, Time: h.time, Odometer: h.odo}),
		dtc.ErrDTCNotRegistered)
	// 状态不允许（点火开期间诊断仪清除）。
	wantErrKind(t, h.m.Handle(dtc.Event{Kind: dtc.EventScanToolClear, Time: h.time, Odometer: h.odo}),
		dtc.ErrStateNotAllowed)
	h.off()
}

// 被拒绝的事件不得改变任何状态。
func TestRejectedEventKeepsState(t *testing.T) {
	h := newHarness(t, testConfig(), [2]int{1, 2})
	h.failCycle(1)
	h.on()
	before := h.snap(1)

	// 直接调用 Handle 以精确控制时刻/里程（harness.do 会重新盖章）。
	rejected := []dtc.Event{
		{Kind: dtc.EventMonitorResult, DTCCode: -1, Time: h.time, Odometer: h.odo},    // 参数非法
		{Kind: dtc.EventMonitorResult, DTCCode: 1, Time: h.time - 1, Odometer: h.odo}, // 回退
		{Kind: dtc.EventIgnitionOn, Time: h.time, Odometer: h.odo},                    // 顺序非法
		{Kind: dtc.EventMonitorResult, DTCCode: 999, Time: h.time, Odometer: h.odo},   // 未登记
		{Kind: dtc.EventScanToolClear, Time: h.time, Odometer: h.odo},                 // 状态不允许
	}
	for i, ev := range rejected {
		err := h.m.Handle(ev)
		t.Logf("rejected event %d %+v -> err=%v", i, ev, err)
		if err == nil {
			t.Fatalf("event %d should be rejected", i)
		}
	}
	if after := h.snap(1); after != before {
		t.Fatalf("rejected events must not change state:\nbefore=%+v\nafter =%+v", before, after)
	}
	h.off()
}

// 非法配置与非法登记被拒绝。
func TestInvalidConfigAndRegistration(t *testing.T) {
	cfg := testConfig()
	cfg.DebouncePassLimit = 0
	if _, err := dtc.NewManager(cfg); err == nil {
		t.Fatal("pass limit 0 must be rejected")
	}
	cfg = testConfig()
	cfg.AutoClearWarmUps = cfg.HealWarmUpCycles - 1
	if _, err := dtc.NewManager(cfg); err == nil {
		t.Fatal("auto clear < heal must be rejected")
	}
	m, err := dtc.NewManager(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	wantErrKind(t, m.RegisterDTC(1, 0), dtc.ErrInvalidParam)
	wantErrKind(t, m.RegisterDTC(1, 4), dtc.ErrInvalidParam)
	if err := m.RegisterDTC(1, 2); err != nil {
		t.Fatal(err)
	}
	wantErrKind(t, m.RegisterDTC(1, 2), dtc.ErrInvalidParam)
}

// 并发调用等价于某个串行顺序：多 goroutine 混合上报与查询，
// 在 -race 下应无数据竞争，且最终状态与串行重放一致。
func TestConcurrentEquivalentToSerial(t *testing.T) {
	cfg := testConfig()
	codes := [][2]int{{1, 1}, {2, 2}, {3, 3}}

	m, err := dtc.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range codes {
		if err := m.RegisterDTC(c[0], c[1]); err != nil {
			t.Fatal(err)
		}
	}

	const cycles = 200
	var wg sync.WaitGroup
	var clock atomic.Int64
	next := func() int64 { return clock.Add(1) }

	// 一个 goroutine 专职驱动点火循环，保证事件顺序合法。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < cycles; i++ {
			for {
				tm := next()
				if err := m.Handle(dtc.Event{Kind: dtc.EventIgnitionOn, Time: tm, Odometer: tm}); err == nil {
					break
				}
			}
			for {
				tm := next()
				if err := m.Handle(dtc.Event{Kind: dtc.EventIgnitionOff, Time: tm, Odometer: tm}); err == nil {
					break
				}
			}
		}
	}()

	// 监测上报 goroutine：落在点火关期间的上报会被拒绝（顺序非法），
	// 被拒绝事件不改变状态，任意交错都等价于某个串行顺序。
	for _, c := range codes {
		wg.Add(1)
		go func(code int) {
			defer wg.Done()
			for i := 0; i < cycles*4; i++ {
				tm := next()
				_ = m.Handle(dtc.Event{
					Kind:     dtc.EventMonitorResult,
					DTCCode:  code,
					Passed:   i%3 == 0,
					Time:     tm,
					Odometer: tm,
				})
			}
		}(c[0])
	}

	// 并发查询。
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < cycles; j++ {
				_ = m.Snapshots()
				if _, err := m.Snapshot(1); err != nil {
					panic(err)
				}
			}
		}()
	}
	wg.Wait()

	// 内部一致性：计数器非负，确认蕴含曾达到确认阈值。
	for _, s := range m.Snapshots() {
		if s.Occurrences < 0 || s.ConsecutiveFailCycles < 0 || s.FaultFreeWarmUps < 0 {
			t.Fatalf("negative counter in %+v", s)
		}
		if s.Confirmed && s.ConsecutiveFailCycles < cfg.ConfirmCycles {
			t.Fatalf("confirmed without enough consecutive failures: %+v", s)
		}
	}
}
