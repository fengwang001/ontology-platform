package delay

import (
	"errors"
	"testing"
)

func mustEngine(t *testing.T, fs []Flight, cfg Config) *Engine {
	t.Helper()
	e, err := New(fs, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func get(t *testing.T, e *Engine, id string) Result {
	t.Helper()
	r, err := e.Get(id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return r
}

// 宵禁左闭右开：起飞恰在起点须推迟，恰在终点可起飞；到达同理。
func TestCurfewBoundaries(t *testing.T) {
	fs := []Flight{
		{ID: "F0", Scheduled: 100, Duration: 10, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
	}
	cfg := Config{MinTurnaround: 0, MinConnection: 0, DutyLimit: 100000,
		Curfews: map[string]Interval{"X": {Start: 100, End: 120}}}

	e := mustEngine(t, fs, cfg)
	if r := get(t, e, "F0"); r.Status != StatusDelayed || r.ActualDep != 120 || r.ActualArr != 130 {
		t.Fatalf("dep left-closed: %+v", r)
	}

	fs[0].Scheduled = 120
	e = mustEngine(t, fs, cfg)
	if r := get(t, e, "F0"); r.Status != StatusScheduled || r.ActualDep != 120 {
		t.Fatalf("dep right-open: %+v", r)
	}

	cfgArr := Config{DutyLimit: 100000,
		Curfews: map[string]Interval{"Y": {Start: 110, End: 130}}}
	fs[0].Scheduled = 100
	e = mustEngine(t, fs, cfgArr)
	if r := get(t, e, "F0"); r.Status != StatusCanceled || r.Reason != ReasonCurfew {
		t.Fatalf("arr left-closed: %+v", r)
	}
	fs[0].Scheduled = 120
	e = mustEngine(t, fs, cfgArr)
	if r := get(t, e, "F0"); r.Status != StatusScheduled || r.ActualArr != 130 {
		t.Fatalf("arr right-open: %+v", r)
	}
}

// 到达宵禁取消沿飞机链与机组链各传导至少两段。
func TestCurfewCascadeTwoChains(t *testing.T) {
	fs := []Flight{
		{ID: "F0", Scheduled: 100, Duration: 20, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
		{ID: "F1", Scheduled: 200, Duration: 10, Origin: "Y", Dest: "Z", AircraftID: "A", CrewID: "D"},
		{ID: "F2", Scheduled: 300, Duration: 10, Origin: "Z", Dest: "X", AircraftID: "A", CrewID: "D"},
		{ID: "G1", Scheduled: 210, Duration: 10, Origin: "Y", Dest: "W", AircraftID: "B1", CrewID: "C"},
		{ID: "G2", Scheduled: 320, Duration: 10, Origin: "W", Dest: "X", AircraftID: "B2", CrewID: "C"},
	}
	cfg := Config{MinTurnaround: 30, MinConnection: 20, DutyLimit: 100000,
		Curfews: map[string]Interval{"Y": {Start: 115, End: 140}}}
	e := mustEngine(t, fs, cfg)

	cases := []struct {
		id     string
		reason Reason
	}{
		{"F0", ReasonCurfew},
		{"F1", ReasonNoAircraft},
		{"F2", ReasonNoAircraft},
		{"G1", ReasonNoCrew},
		{"G2", ReasonNoCrew},
	}
	for _, tc := range cases {
		if r := get(t, e, tc.id); r.Status != StatusCanceled || r.Reason != tc.reason {
			t.Fatalf("%s: %+v want reason %v", tc.id, r, tc.reason)
		}
	}
}

// 取消传导在当前所在机场与后续段起飞机场相同时停止。
func TestPropagationStopsAtSameAirport(t *testing.T) {
	fs := []Flight{
		{ID: "F0", Scheduled: 100, Duration: 20, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
		{ID: "F1", Scheduled: 200, Duration: 10, Origin: "Y", Dest: "Z", AircraftID: "A", CrewID: "C"},
		// 计划链绕回 Y：F1 取消后资源停在 Y，F2 计划从 Z 出发仍被取消，
		// F3 起飞机场恰为 Y（= 当前所在），传导在此停止、F3 正常执行。
		{ID: "F2", Scheduled: 300, Duration: 10, Origin: "Z", Dest: "Y", AircraftID: "A", CrewID: "C"},
		{ID: "F3", Scheduled: 400, Duration: 10, Origin: "Y", Dest: "W", AircraftID: "A", CrewID: "C"},
	}
	cfg := Config{MinTurnaround: 30, MinConnection: 20, DutyLimit: 100000}
	e := mustEngine(t, fs, cfg)
	if err := e.InjectCancel(0, "F1", "r1"); err != nil {
		t.Fatal(err)
	}
	if r := get(t, e, "F1"); r.Status != StatusCanceled || r.Reason != ReasonInjected {
		t.Fatalf("F1: %+v", r)
	}
	if r := get(t, e, "F3"); r.Status == StatusCanceled || r.ActualDep != 400 {
		t.Fatalf("F3 should operate on plan: %+v", r)
	}
}

// 值勤跨度恰等于上限允许，多一分钟取消并截断机组链。
func TestDutyLimitBoundary(t *testing.T) {
	cfg := Config{DutyLimit: 100}
	fs := []Flight{{ID: "F0", Scheduled: 0, Duration: 100, Origin: "X", Dest: "Y",
		AircraftID: "A", CrewID: "C"}}
	e := mustEngine(t, fs, cfg)
	if r := get(t, e, "F0"); r.Status != StatusScheduled {
		t.Fatalf("equal limit: %+v", r)
	}

	fs[0].Duration = 101
	e = mustEngine(t, fs, cfg)
	if r := get(t, e, "F0"); r.Status != StatusCanceled || r.Reason != ReasonDutyExceeded {
		t.Fatalf("over limit: %+v", r)
	}

	fs2 := []Flight{
		{ID: "F0", Scheduled: 0, Duration: 50, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
		{ID: "F1", Scheduled: 60, Duration: 50, Origin: "Y", Dest: "Z", AircraftID: "A", CrewID: "C"},
	}
	e = mustEngine(t, fs2, cfg)
	if r := get(t, e, "F0"); r.Status != StatusScheduled {
		t.Fatalf("seg1: %+v", r)
	}
	if r := get(t, e, "F1"); r.Status != StatusCanceled || r.Reason != ReasonDutyExceeded {
		t.Fatalf("seg2 duty: %+v", r)
	}
}

// 飞机链与机组链交叉：同一注入集合经不同调用次序到达，结论一致。
func TestOrderIndependence(t *testing.T) {
	fs := []Flight{
		{ID: "F0", Scheduled: 100, Duration: 30, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
		{ID: "F1", Scheduled: 200, Duration: 30, Origin: "Y", Dest: "Z", AircraftID: "A", CrewID: "D"},
		{ID: "F2", Scheduled: 300, Duration: 30, Origin: "Z", Dest: "Y", AircraftID: "B", CrewID: "D"},
		{ID: "F3", Scheduled: 400, Duration: 30, Origin: "Y", Dest: "X", AircraftID: "B", CrewID: "C"},
	}
	cfg := Config{MinTurnaround: 40, MinConnection: 40, DutyLimit: 100000}
	build := func(ops func(e *Engine)) map[string]Result {
		e := mustEngine(t, fs, cfg)
		ops(e)
		out := map[string]Result{}
		for _, f := range fs {
			out[f.ID] = get(t, e, f.ID)
		}
		return out
	}
	seq1 := build(func(e *Engine) {
		if err := e.InjectDelay(0, "F0", "d", 80); err != nil {
			t.Fatal(err)
		}
		if err := e.InjectCancel(0, "F2", "x"); err != nil {
			t.Fatal(err)
		}
	})
	seq2 := build(func(e *Engine) {
		if err := e.InjectCancel(0, "F2", "x"); err != nil {
			t.Fatal(err)
		}
		if err := e.InjectDelay(0, "F0", "d", 80); err != nil {
			t.Fatal(err)
		}
	})
	for id, r := range seq1 {
		if r != seq2[id] {
			t.Fatalf("flight %s differs by order: %+v vs %+v", id, r, seq2[id])
		}
	}
}

// 撤回后结论完全恢复；撤回不存在引用报错。
func TestWithdrawRestores(t *testing.T) {
	fs := []Flight{
		{ID: "F0", Scheduled: 100, Duration: 20, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
		{ID: "F1", Scheduled: 200, Duration: 20, Origin: "Y", Dest: "Z", AircraftID: "A", CrewID: "C"},
	}
	cfg := Config{MinTurnaround: 30, MinConnection: 30, DutyLimit: 100000}
	e := mustEngine(t, fs, cfg)
	before := map[string]Result{"F0": get(t, e, "F0"), "F1": get(t, e, "F1")}

	if err := e.InjectDelay(0, "F0", "d", 100); err != nil {
		t.Fatal(err)
	}
	if err := e.InjectCancel(0, "F1", "x"); err != nil {
		t.Fatal(err)
	}
	if err := e.Withdraw(10, "F0", "d"); err != nil {
		t.Fatal(err)
	}
	if err := e.Withdraw(10, "F1", "x"); err != nil {
		t.Fatal(err)
	}
	for id, want := range before {
		if got := get(t, e, id); got != want {
			t.Fatalf("%s not restored: got %+v want %+v", id, got, want)
		}
	}
	if err := e.Withdraw(11, "F0", "d"); !errors.Is(err, ErrNoInjection) {
		t.Fatalf("want ErrNoInjection, got %v", err)
	}
}

// 已起飞航班不可改且时刻固定；被拒操作不推进时钟；时钟回退报错。
func TestDepartedFrozenAndRejections(t *testing.T) {
	fs := []Flight{
		{ID: "F0", Scheduled: 100, Duration: 20, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
		{ID: "F1", Scheduled: 200, Duration: 20, Origin: "Y", Dest: "Z", AircraftID: "A", CrewID: "C"},
	}
	cfg := Config{MinTurnaround: 30, MinConnection: 30, DutyLimit: 100000}
	e := mustEngine(t, fs, cfg)

	if err := e.InjectDelay(100, "F0", "d", 50); !errors.Is(err, ErrDeparted) {
		t.Fatalf("want ErrDeparted, got %v", err)
	}
	if err := e.InjectDelay(90, "F1", "d", 40); err != nil {
		t.Fatalf("rejected op must not advance clock: %v", err)
	}
	if err := e.InjectCancel(80, "F1", "x"); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("want ErrClockRewind, got %v", err)
	}
	if r := get(t, e, "F0"); r.ActualDep != 100 || r.Status != StatusScheduled {
		t.Fatalf("F0 must stay fixed: %+v", r)
	}
	if _, err := e.Get("ZZ"); !errors.Is(err, ErrNoFlight) {
		t.Fatalf("want ErrNoFlight, got %v", err)
	}
	if err := e.InjectCancel(95, "ZZ", "x"); !errors.Is(err, ErrNoFlight) {
		t.Fatalf("want ErrNoFlight, got %v", err)
	}
}

// 参数非法：链条机场不连续、负时刻、非正时长、坏宵禁区间。
func TestInvalidTables(t *testing.T) {
	good := Config{MinTurnaround: 1, MinConnection: 1, DutyLimit: 100}
	cases := []struct {
		name string
		fs   []Flight
		cfg  Config
	}{
		{"aircraft gap", []Flight{
			{ID: "a", Scheduled: 0, Duration: 1, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
			{ID: "b", Scheduled: 2, Duration: 1, Origin: "Z", Dest: "W", AircraftID: "A", CrewID: "D"},
		}, good},
		{"crew gap", []Flight{
			{ID: "a", Scheduled: 0, Duration: 1, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
			{ID: "b", Scheduled: 2, Duration: 1, Origin: "Z", Dest: "W", AircraftID: "B", CrewID: "C"},
		}, good},
		{"negative time", []Flight{
			{ID: "a", Scheduled: -1, Duration: 1, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
		}, good},
		{"bad duration", []Flight{
			{ID: "a", Scheduled: 0, Duration: 0, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
		}, good},
		{"dup id", []Flight{
			{ID: "a", Scheduled: 0, Duration: 1, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
			{ID: "a", Scheduled: 2, Duration: 1, Origin: "Y", Dest: "Z", AircraftID: "B", CrewID: "D"},
		}, good},
		{"bad curfew", []Flight{
			{ID: "a", Scheduled: 0, Duration: 1, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
		}, Config{DutyLimit: 100, Curfews: map[string]Interval{"X": {Start: 20, End: 10}}}},
		{"negative turnaround", []Flight{
			{ID: "a", Scheduled: 0, Duration: 1, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
		}, Config{MinTurnaround: -1, DutyLimit: 100}},
	}
	for _, tc := range cases {
		if _, err := New(tc.fs, tc.cfg); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: want ErrInvalid, got %v", tc.name, err)
		}
	}

	e := mustEngine(t, []Flight{
		{ID: "a", Scheduled: 0, Duration: 1, Origin: "X", Dest: "Y", AircraftID: "A", CrewID: "C"},
	}, good)
	if err := e.InjectDelay(5, "a", "d", -3); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative delay: want ErrInvalid, got %v", err)
	}
}
