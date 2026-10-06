package traffic

import (
	"errors"
	"math"
	"testing"
)

const vehLen = 1.0

// chainNetwork 车流方向 L3 -> L2 -> L1（编号沿上游方向递减）：
// n1 -L3-> n2 -L2-> n3 -L1-> n4。到达 40，基准能力 100，正常态不排队。
func chainNetwork() *Network {
	links := []Link{
		{ID: 1, From: 3, To: 4, Length: 1000, Capacity: 100, Arrival: 40},
		{ID: 2, From: 2, To: 3, Length: 1000, Capacity: 100, Arrival: 40},
		{ID: 3, From: 1, To: 2, Length: 1000, Capacity: 100, Arrival: 40},
	}
	net, err := BuildNetwork(links, Config{VehicleLength: vehLen})
	if err != nil {
		panic(err)
	}
	return net
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustQuery(t *testing.T, s *Service, id int) LinkState {
	t.Helper()
	st, err := s.Query(id)
	if err != nil {
		t.Fatalf("query %d: %v", id, err)
	}
	return st
}

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// 排队恰达到路段长度那一刻回溢：下游 L1 满 -> 直接上游 L2 立即限为二级。
func TestSpillbackAtExactMoment(t *testing.T) {
	s := New(chainNetwork(), nil)
	must(t, s.Register(10, 1, 0, 0, 1.0)) // L1 完全封锁，速率 +40
	fullAt := 1000.0 / 40.0

	must(t, s.Advance(fullAt))
	st1, st2 := mustQuery(t, s, 1), mustQuery(t, s, 2)
	if !approx(st1.Queue, 1000, 1e-7) || st1.Level != 1 {
		t.Fatalf("L1 exactly full & level1: %+v", st1)
	}
	if st2.Level != 2 || !approx(st2.EffectiveCap, 0, 1e-9) {
		t.Fatalf("L2 level2 capped to incident cap 0: %+v", st2)
	}
	must(t, s.Advance(fullAt+1))
	if q := mustQuery(t, s, 2).Queue; !approx(q, 40, 1e-7) {
		t.Fatalf("upstream L2 queue grows at 40/s: %v", q)
	}
	// 回溢不沿下游传播：L3 此刻尚不受影响。
	if st3 := mustQuery(t, s, 3); st3.Level != 0 {
		t.Fatalf("L3 not yet affected: %+v", st3)
	}
}

// 同一路段两个同时事件取最大削减。
func TestTwoIncidentsTakeMaxReduction(t *testing.T) {
	s := New(chainNetwork(), nil)
	must(t, s.Register(1, 1, 0, 0, 0.3)) // cap70，到达40，不排队
	must(t, s.Register(2, 1, 0, 0, 1.0)) // cap0 更严，速率 +40
	must(t, s.Advance(5))
	if st := mustQuery(t, s, 1); !approx(st.EffectiveCap, 0, 1e-9) ||
		!approx(st.Queue, 200, 1e-7) {
		t.Fatalf("cap0 q200: %+v", st)
	}
	must(t, s.Resolve(2, 5)) // 剩 0.3 事件，cap70，速率 -30 开始消散
	must(t, s.Advance(6))
	if st := mustQuery(t, s, 1); !approx(st.EffectiveCap, 70, 1e-9) ||
		!approx(st.Queue, 170, 1e-7) {
		t.Fatalf("after resolve cap70 q170: %+v", st)
	}
}

// 事件解除恰与回溢同一时刻：不超限、不残留等级，随即以富余能力消散。
func TestResolveSameMomentAsSpillback(t *testing.T) {
	s := New(chainNetwork(), nil)
	must(t, s.Register(1, 1, 0, 0, 1.0))
	fullAt := 1000.0 / 40.0
	must(t, s.Resolve(1, fullAt))
	st1, st2 := mustQuery(t, s, 1), mustQuery(t, s, 2)
	if st1.Queue > 1000+1e-7 {
		t.Fatalf("queue exceeds length %v", st1.Queue)
	}
	if st1.Level != 0 || st2.Level != 0 || !approx(st2.EffectiveCap, 100, 1e-9) {
		t.Fatalf("levels/caps wrong: %+v %+v", st1, st2)
	}
	must(t, s.Advance(fullAt+1))
	if q := mustQuery(t, s, 1).Queue; !approx(q, 940, 1e-7) { // 40-100=-60
		t.Fatalf("dissipate at 60/s: %v", q)
	}
}

// 排队消散到零恰与新事件开始同一时刻。
func TestDissipateToZeroSameMomentAsNewIncident(t *testing.T) {
	s := New(chainNetwork(), nil)
	must(t, s.Register(1, 1, 0, 0, 1.0)) // 速率 +40
	must(t, s.Advance(15))               // q600
	must(t, s.Resolve(1, 15))            // 速率 -60，10 个时间单位后落零
	zeroAt := 25.0
	must(t, s.Register(2, 1, zeroAt, zeroAt, 0.7)) // cap30，速率 40-30=+10
	must(t, s.Advance(zeroAt))
	if st := mustQuery(t, s, 1); !approx(st.Queue, 0, 1e-7) ||
		!approx(st.EffectiveCap, 30, 1e-9) || st.Level != 1 {
		t.Fatalf("zero then new incident: %+v", st)
	}
	must(t, s.Advance(zeroAt+1))
	if q := mustQuery(t, s, 1).Queue; !approx(q, 10, 1e-7) {
		t.Fatalf("regrow at 10/s: %v", q)
	}
}

// 菱形：同一路段被两条下游路径影响取最小等级。
//
//	n0 -L1-> n2 -L2-> n3 -L3-> n4 -L5(事件)-> n6
//	 |                 L6 --------> n4
//	 L7 --------------------------> n4
//
// L5 满: L3 与 L6、L7 同为 2 级；L1 经 L2->L3 为 4 级、经 L7 为 3 级，取 3。
func TestDiamondTakesMinimumLevel(t *testing.T) {
	links := []Link{
		{ID: 1, From: 0, To: 2, Length: 100, Capacity: 100, Arrival: 100},
		{ID: 2, From: 2, To: 3, Length: 100, Capacity: 100, Arrival: 100},
		{ID: 3, From: 3, To: 4, Length: 100, Capacity: 100, Arrival: 100},
		{ID: 5, From: 4, To: 6, Length: 1000, Capacity: 100, Arrival: 100}, // 事件段
		{ID: 6, From: 2, To: 4, Length: 100, Capacity: 100, Arrival: 100},  // 中路径
		{ID: 7, From: 0, To: 4, Length: 100, Capacity: 100, Arrival: 100},  // 短路径
	}
	net, err := BuildNetwork(links, Config{VehicleLength: 1})
	if err != nil {
		t.Fatal(err)
	}
	s := New(net, nil)
	must(t, s.Register(1, 5, 0, 0, 1.0))
	must(t, s.Advance(100))
	lv := func(id int) int { return mustQuery(t, s, id).Level }
	if lv(5) != 1 || lv(3) != 2 || lv(6) != 2 || lv(7) != 2 || lv(1) != 3 {
		t.Fatalf("levels: 5=%d 3=%d 6=%d 7=%d 1=%d",
			lv(5), lv(3), lv(6), lv(7), lv(1))
	}
}

// 环路：回溢沿环回到起点不重复限制、不陷入死循环。
func TestCycleDoesNotDoubleRestrict(t *testing.T) {
	links := []Link{
		{ID: 1, From: 1, To: 2, Length: 1000, Capacity: 100, Arrival: 100},
		{ID: 2, From: 2, To: 3, Length: 1000, Capacity: 100, Arrival: 100},
		{ID: 3, From: 3, To: 1, Length: 1000, Capacity: 100, Arrival: 100},
	}
	net, err := BuildNetwork(links, Config{VehicleLength: 1})
	if err != nil {
		t.Fatal(err)
	}
	s := New(net, nil)
	must(t, s.Register(1, 2, 0, 0, 0.4)) // L2 有效能力 60
	must(t, s.Advance(300))
	for _, id := range []int{1, 2, 3} {
		st := mustQuery(t, s, id)
		if st.Queue > 1000+1e-6 {
			t.Fatalf("link %d queue exceeds length %v", id, st.Queue)
		}
		if st.EffectiveCap < 60-1e-6 {
			t.Fatalf("link %d cap below incident-imposed minimum %v", id, st.EffectiveCap)
		}
	}
}

// 一次推进与分多次推进逐字段一致。
func TestSingleVsMultipleAdvanceIdentical(t *testing.T) {
	setup := func(s *Service) {
		must(t, s.Register(1, 1, 0, 0, 0.7))
		must(t, s.Register(2, 2, 0, 3, 0.5))
	}

	s1 := New(chainNetwork(), nil)
	setup(s1)
	must(t, s1.Advance(25))
	must(t, s1.Resolve(1, 40))
	must(t, s1.UpdateReduction(2, 60, 0.2))
	must(t, s1.Advance(105))

	s2 := New(chainNetwork(), nil)
	setup(s2)
	for _, tt := range []float64{1.5, 7, 12.33, 25, 30, 40} {
		must(t, s2.Advance(tt))
	}
	must(t, s2.Resolve(1, 40))
	must(t, s2.Advance(60))
	must(t, s2.UpdateReduction(2, 60, 0.2))
	for _, tt := range []float64{70.5, 88, 105} {
		must(t, s2.Advance(tt))
	}

	for _, id := range []int{1, 2, 3} {
		a, b := mustQuery(t, s1, id), mustQuery(t, s2, id)
		if a != b {
			t.Fatalf("link %d differs single=%+v multi=%+v", id, a, b)
		}
	}
}

// 错误可区分且按固定次序只报最靠前的一类。
func TestErrorOrdering(t *testing.T) {
	s := New(chainNetwork(), nil)
	cases := []struct {
		want error
		call func() error
	}{
		{want: ErrClockRewind, call: func() error { return s.Advance(-1) }},
		{want: ErrLinkNotFound, call: func() error { return s.Register(1, 999, 0, 0, 0.5) }},
		{want: ErrReductionOutOfRange, call: func() error { return s.Register(1, 1, 0, 0, 1.5) }},
		{want: ErrIncidentNotFound, call: func() error { return s.Resolve(7, 0) }},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Fatalf("want %v got %v", c.want, err)
		}
	}
	must(t, s.Register(1, 1, 0, 0, 0.5))
	must(t, s.Resolve(1, 0))
	if err := s.Resolve(1, 0); !errors.Is(err, ErrIncidentAlreadyEnded) {
		t.Fatalf("want already ended got %v", err)
	}
	if _, err := BuildNetwork([]Link{{ID: 1, From: 1, To: 2, Length: 10,
		Capacity: 5, Arrival: 6}}, Config{VehicleLength: 1}); !errors.Is(err, ErrFlowExceedsCapacity) {
		t.Fatalf("want flow exceeds capacity got %v", err)
	}
	if err := s.Advance(0); err != nil {
		t.Fatalf("equal-time advance allowed: %v", err)
	}
}
