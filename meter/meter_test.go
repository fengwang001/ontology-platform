package meter

import "testing"

func baseCfg() Config {
	return Config{
		CreatedAt:          0,
		WarnThreshold:      50,
		EmergencyThreshold: 0,
		EmergencyAmount:    100,
		RestoreThreshold:   30,
		DebtRepayRatio:     Ratio{N: 1, D: 2},
		ConfirmWindow:      100,
		CycleLength:        1000,
		FriendlyStart:      10 * 3600,
		FriendlyEnd:        12 * 3600,
		Holidays:           map[int64]struct{}{},
		Tariffs:            []Tariff{{Start: 0, Price: 100}},
	}
}

func kinds(evs []Event) []EventKind {
	out := make([]EventKind, len(evs))
	for i, e := range evs {
		out[i] = e.Kind
	}
	return out
}

func hasKind(evs []Event, k EventKind) bool {
	for _, e := range evs {
		if e.Kind == k {
			return true
		}
	}
	return false
}

func countKind(evs []Event, k EventKind) int {
	n := 0
	for _, e := range evs {
		if e.Kind == k {
			n++
		}
	}
	return n
}

func findKind(evs []Event, k EventKind) (Event, bool) {
	for _, e := range evs {
		if e.Kind == k {
			return e, true
		}
	}
	return Event{}, false
}

// 余额恰等于预警阈值不预警；再扣到阈值以下才预警；未回升不重复；阈值变更不触发。
func TestWarnThresholdEqual(t *testing.T) {
	c := New(baseCfg(), 100)
	_, _ = c.AddReading(0, 0)
	evs, err := c.AddReading(10, 500) // 扣 50，余额恰好 50 == 阈值
	if err != nil || hasKind(evs, EvWarn) {
		t.Fatalf("equal threshold must not warn: %v %v", kinds(evs), err)
	}
	evs, _ = c.AddReading(20, 510) // 再扣 1，余额 49
	if !hasKind(evs, EvWarn) {
		t.Fatalf("below threshold must warn: %v", kinds(evs))
	}
	evs, _ = c.AddReading(30, 520)
	if hasKind(evs, EvWarn) {
		t.Fatalf("must not re-warn below: %v", kinds(evs))
	}
	if err := c.SetWarnThreshold(200); err != nil {
		t.Fatal(err)
	}
	evs, _ = c.Recharge(40, 300) // 回到 >= 阈值
	if hasKind(evs, EvWarn) {
		t.Fatalf("returning above must not warn: %v", kinds(evs))
	}
	evs, _ = c.AddReading(50, 2000) // 再扣 147，余额 201；仍高于
	if hasKind(evs, EvWarn) {
		t.Fatalf("still above new threshold: %v", kinds(evs))
	}
	evs, _ = c.AddReading(60, 2020) // 再扣 2，余额 199 < 200
	if !hasKind(evs, EvWarn) {
		t.Fatalf("re-cross must warn: %v", kinds(evs))
	}
}

// 友好时段起点推迟、终点立即执行（左闭右开）；休息日与节假日全天。
func TestFriendlyBoundaries(t *testing.T) {
	start := int64(10 * 3600)
	end := int64(12 * 3600)

	c := New(baseCfg(), 1)
	_, _ = c.AddReading(start-1, 0)
	evs, _ := c.AddReading(start, 10000)
	pc, ok := findKind(evs, EvPendingCut)
	if !ok || pc.ExecuteAt != end {
		t.Fatalf("start defers to end: %+v", evs)
	}
	evs, _ = c.Advance(end - 1)
	if c.Snapshot().Status != PendingCut {
		t.Fatal("still pending before end")
	}
	evs, _ = c.Advance(end)
	if c.Snapshot().Status != Cut || !hasKind(evs, EvCut) {
		t.Fatalf("cut exactly at end: %v", kinds(evs))
	}

	c2 := New(baseCfg(), 1)
	_, _ = c2.AddReading(end-1, 0)
	evs, _ = c2.AddReading(end, 10000)
	if c2.Snapshot().Status != Cut || !hasKind(evs, EvCut) {
		t.Fatalf("end must cut immediately: %v", kinds(evs))
	}

	sat := int64(3 * 86400) // 1970-01-03 星期六
	c3 := New(baseCfg(), 1)
	_, _ = c3.AddReading(sat, 0)
	evs, _ = c3.AddReading(sat+3600, 10000)
	pc, _ = findKind(evs, EvPendingCut)
	if pc.ExecuteAt != sat+86400 {
		t.Fatalf("rest day defers to midnight: %+v", pc)
	}

	cfg := baseCfg()
	cfg.CycleLength = 86400 * 2
	cfg.Holidays[10] = struct{}{}
	c4 := New(cfg, 1)
	_, _ = c4.AddReading(10*86400, 0)
	evs, _ = c4.AddReading(10*86400+3600, 10000)
	pc, _ = findKind(evs, EvPendingCut)
	if pc.ExecuteAt != 11*86400 {
		t.Fatalf("holiday defers to midnight: %+v", pc)
	}
}

// 待停电期间充值使余额非负：取消停电，之后不执行。
func TestPendingCutCanceledByRecharge(t *testing.T) {
	cfg := baseCfg()
	cfg.CycleLength = 86400 * 2
	c := New(cfg, 5)
	_, _ = c.AddReading(35999, 0)
	_, _ = c.AddReading(36000, 1000)
	if c.Snapshot().Status != PendingCut {
		t.Fatal("expected pending cut")
	}
	evs, _ := c.Recharge(37000, 200)
	if c.Snapshot().Status != Powered || !hasKind(evs, EvCutCanceled) {
		t.Fatalf("recharge cancels: %v", kinds(evs))
	}
	evs, _ = c.Advance(43200)
	if c.Snapshot().Status != Powered || hasKind(evs, EvCut) {
		t.Fatalf("canceled must not execute: %v", kinds(evs))
	}
}
