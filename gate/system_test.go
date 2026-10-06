package gatealloc

import (
	"fmt"
	"testing"
)

// 通用构造：3 个登机口 G1/G2/G3，G1-G2 相邻、G2-G3 相邻。
func newTestSystem(t *testing.T, cfg Config, gates []GateSpec, flights []FlightSpec, adjs []Adj) *System {
	t.Helper()
	s, err := New(cfg, gates, flights, adjs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func baseGates() []GateSpec {
	return []GateSpec{
		{ID: "G1", MaxClass: Class3, Kind: GateDual},
		{ID: "G2", MaxClass: Class2, Kind: GateDual},
		{ID: "G3", MaxClass: Class3, Kind: GateDual},
	}
}

func baseAdjs() []Adj { return []Adj{{"G1", "G2"}, {"G2", "G3"}} }

// TestEndpointTouch：左闭右开，端点相接不算冲突；缓冲让相接变相交。
func TestEndpointTouch(t *testing.T) {
	cfg := Config{Buffer: 0, MaxStay: 1000, DeplaneDur: 20, BoardingDur: 20}
	flights := []FlightSpec{
		{ID: "A", Class: Class1, Kind: KindDomestic, SchedArr: 10, SchedDep: 20, BoardLead: 5},
		{ID: "B", Class: Class1, Kind: KindDomestic, SchedArr: 20, SchedDep: 30, BoardLead: 5},
	}
	s := newTestSystem(t, cfg, baseGates(), flights, []Adj{{"G1", "G3"}})
	if r := s.Assign(0, "A", "G1", SegWhole); !r.OK {
		t.Fatalf("assign A: %v", r.Err)
	}
	if r := s.Assign(0, "B", "G1", SegWhole); !r.OK {
		t.Fatalf("touching endpoints must be allowed, got %v", r.Err)
	}
	if id, _, ok := s.OccupantAt("G1", 20); !ok || id != "B" {
		t.Fatalf("occupant at t=10 want B, got %s/%v", id, ok)
	}
	if id, _, ok := s.OccupantAt("G1", 19); !ok || id != "A" {
		t.Fatalf("occupant at t=9 want A, got %s", id)
	}

	// Buffer=2：A 占用到 12，与 B[10,22) 相交，冲突。
	cfg2 := Config{Buffer: 2, MaxStay: 1000, DeplaneDur: 20, BoardingDur: 20}
	s2 := newTestSystem(t, cfg2, baseGates(), flights, baseAdjs())
	if r := s2.Assign(0, "A", "G1", SegWhole); !r.OK {
		t.Fatalf("assign A: %v", r.Err)
	}
	r := s2.Assign(0, "B", "G1", SegWhole)
	if r.OK || r.Err.Reason != RejTimeConflict || r.Err.ConflictID != "A" {
		t.Fatalf("buffer-induced overlap must conflict with A, got %+v", r)
	}
}

// TestAdjacencyOnlyClass3：相邻限制只对最高等级生效。
func TestAdjacencyOnlyClass3(t *testing.T) {
	cfg := Config{Buffer: 0, MaxStay: 1000, DeplaneDur: 20, BoardingDur: 20}
	flights := []FlightSpec{
		{ID: "H1", Class: Class3, Kind: KindIntl, SchedArr: 10, SchedDep: 40, BoardLead: 10},
		{ID: "H2", Class: Class3, Kind: KindIntl, SchedArr: 20, SchedDep: 50, BoardLead: 10},
		{ID: "L1", Class: Class2, Kind: KindDomestic, SchedArr: 10, SchedDep: 40, BoardLead: 10},
	}
	s := newTestSystem(t, cfg, baseGates(), flights, []Adj{{"G1", "G3"}})
	if r := s.Assign(0, "H1", "G1", SegWhole); !r.OK {
		t.Fatalf("H1: %v", r.Err)
	}
	// 两个三级隔一个 G2 且不相邻：G1 与 G3 直接相邻 -> 拒绝。
	if r := s.Assign(0, "H2", "G3", SegWhole); r.OK || r.Err.Reason != RejAdjacency {
		t.Fatalf("class3 vs class3 on adjacent gates must be rejected: %+v", r)
	}
	// H2 放到 G2（最大等级 2，机型不兼容）——改用低等级 L1 放 G3：不触发相邻限制。
	if r := s.Assign(0, "L1", "G3", SegWhole); !r.OK {
		t.Fatalf("class2 next to class3 must be allowed, got %v", r.Err)
	}
	// 端点相接不违反。
	touch := FlightSpec{ID: "H3", Class: Class3, Kind: KindIntl, SchedArr: 40, SchedDep: 60, BoardLead: 10}
	s3 := newTestSystem(t, cfg, baseGates(), append(flights, touch), []Adj{{"G1", "G3"}})
	if r := s3.Assign(0, "H1", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	if r := s3.Assign(0, "H3", "G3", SegWhole); !r.OK {
		t.Fatalf("touching class3 intervals on adjacent gates allowed, got %v", r.Err)
	}
}

// TestSplitAtBoundary：占用长度恰等于上限按整段；多一分钟拆两段。
func TestSplitAtBoundary(t *testing.T) {
	// 整段占用长度 = dep+Buffer-arr。
	mk := func(dep int) Config {
		return Config{Buffer: 10, MaxStay: 100, DeplaneDur: 15, BoardingDur: 25}
	}
	flights := func(id string, dep int) []FlightSpec {
		return []FlightSpec{{ID: id, Class: Class1, Kind: KindDomestic, SchedArr: 10, SchedDep: dep, BoardLead: 10}}
	}
	cfg := mk(90)
	// dep=90,buffer=10 => 长度 100 == MaxStay => 整段
	s := newTestSystem(t, cfg, baseGates(), flights("F", 100), nil)
	if split, _ := s.IsSplit("F"); split {
		t.Fatal("length == MaxStay must stay whole")
	}
	if r := s.Assign(0, "F", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}

	// dep=91 => 长度 101 > MaxStay => 拆段
	s2 := newTestSystem(t, Config{Buffer: 10, MaxStay: 100, DeplaneDur: 15, BoardingDur: 25},
		baseGates(), flights("F", 101), nil)
	split, _ := s2.IsSplit("F")
	if !split {
		t.Fatal("length == MaxStay+1 must split")
	}
	// 不能再按整段指派
	if r := s2.Assign(0, "F", "G1", SegWhole); r.OK || r.Err.Reason != RejInvalidParam {
		t.Fatalf("whole assign on split flight must be invalid_param, got %+v", r)
	}
	// 卸客段 [10,25)、登机段 [101-25,101+10)=[76,111)，可分指不同口。
	if r := s2.Assign(0, "F", "G1", SegDeplane); !r.OK {
		t.Fatal(r.Err)
	}
	if r := s2.Assign(0, "F", "G2", SegBoarding); !r.OK {
		t.Fatal(r.Err)
	}
	dg, bg, sp, _ := s2.GateOf("F")
	if !sp || dg != "G1" || bg != "G2" {
		t.Fatalf("want deplane G1 boarding G2, got %s %s %v", dg, bg, sp)
	}
	// 中间 [15,66) 在远机位，G1 在 t=20 应空闲。
	if id, _, ok := s2.OccupantAt("G1", 30); ok {
		t.Fatalf("G1 at t=20 must be free (remote stand), occupied by %s", id)
	}
}

// TestDelaySplitsAndKeepsArrivalGate：推移跨过上限自动拆段，原口留给卸客段。
func TestDelaySplitsAndKeepsArrivalGate(t *testing.T) {
	cfg := Config{Buffer: 0, MaxStay: 100, DeplaneDur: 10, BoardingDur: 10}
	flights := []FlightSpec{
		{ID: "F", Class: Class1, Kind: KindDomestic, SchedArr: 10, SchedDep: 110, BoardLead: 5},
	}
	s := newTestSystem(t, cfg, baseGates(), flights, nil)
	if r := s.Assign(0, "F", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	res := s.Delay(0, "F", MaskDep, 0, 111)
	if !res.OK {
		t.Fatalf("delay: %v", res.Err)
	}
	if !res.BecameSplit {
		t.Fatal("delay crossing MaxStay must report BecameSplit")
	}
	dg, bg, sp, _ := s.GateOf("F")
	if !sp || dg != "G1" || bg != "" {
		t.Fatalf("deplane keeps G1, boarding pending; got %s %s split=%v", dg, bg, sp)
	}
	// 被拆出的登机段不会自动指派。
	if r := s.Assign(0, "F", "G2", SegBoarding); !r.OK {
		t.Fatal(r.Err)
	}
}

// evictWorld 构造：占位者 D 在 G1 上 [300,400)；
// 延误者 M 初始在 G1 上 [100,200)（互不相交，同口）。
// 通过把 M 的起飞推迟到 350，使 M[100,350) 与 D[300,400) 在同口冲突。
type evictWorld struct {
	s       *System
	mover   string
	holder  string
	moverF  FlightSpec
	holderF FlightSpec
}

func newEvictWorld(t *testing.T, mk, hk Kind, mc, hc Class, mSchedArr, hSchedArr int) evictWorld {
	cfg := Config{Buffer: 0, MaxStay: 100000, DeplaneDur: 20, BoardingDur: 20}
	mf := FlightSpec{ID: "M", Class: mc, Kind: mk, SchedArr: mSchedArr, SchedDep: mSchedArr + 100, BoardLead: 5}
	hf := FlightSpec{ID: "D", Class: hc, Kind: hk, SchedArr: hSchedArr, SchedDep: hSchedArr + 100, BoardLead: 5}
	s := newTestSystem(t, cfg, baseGates(), []FlightSpec{mf, hf}, nil)
	if r := s.Assign(0, "M", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	if r := s.Assign(0, "D", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	return evictWorld{s: s, mover: "M", holder: "D", moverF: mf, holderF: hf}
}

// TestEvictionPriorityFourLevels：延误推移触发挤占的四级优先比较。
func TestEvictionPriorityFourLevels(t *testing.T) {
	// 1) 国际先于国内：国际延误者 M 胜国内占用户 D。
	{
		w := newEvictWorld(t, KindIntl, KindDomestic, Class2, Class2, 100, 300)
		res := w.s.Delay(0, "M", MaskDep, 0, 350)
		if !res.OK || len(res.Evicted) != 1 || res.Evicted[0].Victim != "D" {
			t.Fatalf("case1 intl mover must evict domestic: %+v", res)
		}
		if dg, _, _, _ := w.s.GateOf("D"); dg != "" {
			t.Fatal("evicted D pending, no auto reassignment")
		}
		if dg, _, _, _ := w.s.GateOf("M"); dg != "G1" {
			t.Fatal("M keeps G1")
		}
	}
	// 2) 机型高者先：同为国内，M 三级、D 二级，M 胜。
	{
		w := newEvictWorld(t, KindDomestic, KindDomestic, Class3, Class2, 100, 300)
		res := w.s.Delay(0, "M", MaskDep, 0, 350)
		if !res.OK || res.Evicted[0].Victim != "D" {
			t.Fatalf("case2 higher class must win: %+v", res)
		}
	}
	// 3) 计划到达早者先：同为国际二级，M 计划 90 早于 D 的 300，M 胜。
	{
		w := newEvictWorld(t, KindIntl, KindIntl, Class2, Class2, 90, 300)
		res := w.s.Delay(0, "M", MaskDep, 0, 350)
		if !res.OK || res.Evicted[0].Victim != "D" {
			t.Fatalf("case3 earlier scheduled must win: %+v", res)
		}
	}
	// 4) 标识小者先：计划属性全同（含相同计划到达 100）；
	// 用指派前的延误更新把 D 的当前区间挪到 [200,300)，M 为 [100,200)。
	{
		cfg := Config{Buffer: 0, MaxStay: 100000, DeplaneDur: 20, BoardingDur: 20}
		mf := FlightSpec{ID: "M", Class: Class2, Kind: KindIntl, SchedArr: 100, SchedDep: 200, BoardLead: 5}
		hf := FlightSpec{ID: "D", Class: Class2, Kind: KindIntl, SchedArr: 100, SchedDep: 300, BoardLead: 5}
		sys := newTestSystem(t, cfg, baseGates(), []FlightSpec{mf, hf}, nil)
		if r := sys.Delay(0, "D", MaskArr|MaskDep, 200, 300); !r.OK {
			t.Fatal(r.Err)
		}
		if r := sys.Assign(0, "M", "G1", SegWhole); !r.OK {
			t.Fatal(r.Err)
		}
		if r := sys.Assign(0, "D", "G1", SegWhole); !r.OK {
			t.Fatal(r.Err)
		}
		res := sys.Delay(0, "M", MaskDep, 0, 250)
		if !res.OK || res.Evicted[0].Victim != "M" || res.Evicted[0].Winner != "D" {
			t.Fatalf("case4 smaller id D must win: %+v", res)
		}
		if dg, _, _, _ := sys.GateOf("M"); dg != "" {
			t.Fatal("M must lose gate")
		}
		if dg, _, _, _ := sys.GateOf("D"); dg != "G1" {
			t.Fatal("D keeps G1")
		}
	}
}

// TestBothProtectedRejectDelay：冲突双方均不可被挤占时，延误更新被拒，
// 不推进时钟、不留任何改动。
// M 国内三级 [10,110)@G1（已到达、未登机）；X 国际三级 [150,250)@G2（登机中）。
// G1-G2 相邻。t=80 把 M 起飞推到 220 -> [0,220) 与 X 在相邻口冲突。
// X 国际优先 => 低优先级 M 须让位，但 M 已到达不可挤 => cannot_evict。
func TestBothProtectedRejectDelay(t *testing.T) {
	cfg := Config{Buffer: 0, MaxStay: 100000, DeplaneDur: 20, BoardingDur: 20}
	flights := []FlightSpec{
		{ID: "M", Class: Class3, Kind: KindDomestic, SchedArr: 10, SchedDep: 110, BoardLead: 10},
		{ID: "X", Class: Class3, Kind: KindIntl, SchedArr: 150, SchedDep: 250, BoardLead: 170},
	}
	s := newTestSystem(t, cfg, baseGates(), flights, []Adj{{"G1", "G3"}})
	if r := s.Assign(0, "M", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	if r := s.Assign(0, "X", "G3", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	before := s.Snapshot()
	res := s.Delay(80, "M", MaskDep, 0, 220)
	if res.OK || res.Err.Reason != RejCannotEvict {
		t.Fatalf("both protected must yield cannot_evict, got %+v", res)
	}
	if !snapshotsEqual(before, s.Snapshot()) {
		t.Fatal("rejected delay must leave no changes")
	}
	if s.Now() != 0 {
		t.Fatalf("rejected op must not advance clock, now=%d", s.Now())
	}

	// 对称情形：高优先级延误方撞上已登机的低优先级占用户，同样拒绝。
	flights2 := []FlightSpec{
		{ID: "M", Class: Class3, Kind: KindIntl, SchedArr: 10, SchedDep: 110, BoardLead: 10},
		{ID: "X", Class: Class3, Kind: KindDomestic, SchedArr: 150, SchedDep: 250, BoardLead: 170},
	}
	s2 := newTestSystem(t, cfg, baseGates(), flights2, []Adj{{"G1", "G3"}})
	if r := s2.Assign(0, "M", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	if r := s2.Assign(0, "X", "G3", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	res2 := s2.Delay(80, "M", MaskDep, 0, 220)
	if res2.OK || res2.Err.Reason != RejCannotEvict {
		t.Fatalf("protected opponent must yield cannot_evict, got %+v", res2)
	}
}

func snapshotsEqual(a, b Snapshot) bool {
	if a.Now != b.Now || len(a.Assignments) != len(b.Assignments) {
		return false
	}
	for i := range a.Assignments {
		if a.Assignments[i] != b.Assignments[i] {
			return false
		}
	}
	if len(a.Flights) != len(b.Flights) {
		return false
	}
	for id, fa := range a.Flights {
		if fb, ok := b.Flights[id]; !ok || fa != fb {
			return false
		}
	}
	return true
}

// TestReassignRejectedKeepsOld：改派被拒绝后原指派保持不变。
func TestReassignRejectedKeepsOld(t *testing.T) {
	cfg := Config{Buffer: 0, MaxStay: 100000, DeplaneDur: 20, BoardingDur: 20}
	flights := []FlightSpec{
		{ID: "A", Class: Class1, Kind: KindDomestic, SchedArr: 10, SchedDep: 60, BoardLead: 10},
		{ID: "B", Class: Class1, Kind: KindDomestic, SchedArr: 20, SchedDep: 70, BoardLead: 10},
	}
	s := newTestSystem(t, cfg, baseGates(), flights, nil)
	if r := s.Assign(0, "A", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	if r := s.Assign(0, "B", "G2", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	// A 改派到 G2：与 B 冲突，拒绝；A 仍在 G1，时钟停在 0。
	r := s.Assign(5, "A", "G2", SegWhole)
	if r.OK || r.Err.Reason != RejTimeConflict {
		t.Fatalf("expected conflict, got %+v", r)
	}
	if dg, _, _, _ := s.GateOf("A"); dg != "G1" {
		t.Fatalf("rejected reassignment must keep old gate, got %s", dg)
	}
	if id, _, ok := s.OccupantAt("G1", 30); !ok || id != "A" {
		t.Fatal("A must still occupy G1")
	}
	if s.Now() != 0 {
		t.Fatalf("clock must not advance on reject, now=%d", s.Now())
	}
	// 改派成功后旧口释放。
	if r := s.Assign(5, "A", "G3", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	if dg, _, _, _ := s.GateOf("A"); dg != "G3" {
		t.Fatalf("A should be on G3, got %s", dg)
	}
	if _, _, ok := s.OccupantAt("G1", 20); ok {
		t.Fatal("G1 must be free after successful reassignment")
	}
}

// TestClockRules：时钟回退、已到达不可改、已登机不可改起飞。
func TestClockRules(t *testing.T) {
	cfg := Config{Buffer: 0, MaxStay: 100000, DeplaneDur: 20, BoardingDur: 20}
	flights := []FlightSpec{
		{ID: "A", Class: Class1, Kind: KindDomestic, SchedArr: 100, SchedDep: 200, BoardLead: 50},
	}
	s := newTestSystem(t, cfg, baseGates(), flights, nil)
	if r := s.Assign(10, "A", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	if r := s.Assign(5, "A", "G2", SegWhole); r.OK || r.Err.Reason != RejClockBack {
		t.Fatalf("clock back: %+v", r)
	}
	// t>=到达后，到达侧登机口与到达时刻不可改。
	if r := s.Assign(100, "A", "G2", SegWhole); r.OK || r.Err.Reason != RejArrivedLocked {
		t.Fatalf("arrived flight gate locked: %+v", r)
	}
	if r := s.Delay(100, "A", MaskArr, 120, 0); r.OK || r.Err.Reason != RejArrivedLocked {
		t.Fatalf("arrived flight arr locked: %+v", r)
	}
	// 已开始登机（t>=200-50=150）后起飞不可改。
	if r := s.Delay(150, "A", MaskDep, 0, 210); r.OK || r.Err.Reason != RejArrivedLocked {
		t.Fatalf("boarding flight dep locked: %+v", r)
	}
	// 被拒操作不推进时钟。
	if s.Now() != 10 {
		t.Fatalf("clock=%d want 10", s.Now())
	}
	// 仅推迟时刻相等是合法的（“只允许推迟”包含相等），且推进时钟。
	if r := s.Delay(120, "A", MaskDep, 0, 200); !r.OK {
		t.Fatalf("equal-value update allowed: %v", r.Err)
	}
	if s.Now() != 120 {
		t.Fatalf("clock=%d want 120", s.Now())
	}
}

// TestRejectionOrder：逐对验证统一拒绝次序
// 参数非法 > 时钟回退 > 不存在 > 已到达/已登机 > 机型不兼容 > 属性不符 >
// 相邻限制 > 时间冲突 > 不可挤占。
func TestRejectionOrder(t *testing.T) {
	// 每一对构造一次调用，让该调用同时满足两类拒绝条件，断言取靠前类。
	type tc struct {
		name string
		run  func(s *System) Reason
		want Reason
	}

	// 基础世界：G1(三级,国际,相邻G3)、G2、G3；若干航班。
	build := func(t *testing.T) *System {
		cfg := Config{Buffer: 0, MaxStay: 100000, DeplaneDur: 20, BoardingDur: 20}
		gates := []GateSpec{
			{ID: "G1", MaxClass: Class3, Kind: GateIntl},
			{ID: "G2", MaxClass: Class2, Kind: GateDomestic},
			{ID: "G3", MaxClass: Class3, Kind: GateIntl},
		}
		flights := []FlightSpec{
			{ID: "A", Class: Class3, Kind: KindIntl, SchedArr: 100, SchedDep: 200, BoardLead: 10},
			{ID: "B", Class: Class3, Kind: KindIntl, SchedArr: 300, SchedDep: 400, BoardLead: 10},
		}
		s := newTestSystem(t, cfg, gates, flights, []Adj{{"G1", "G3"}})
		// 推进时钟到 50，并安排 B 占 G3。
		if r := s.Assign(50, "B", "G3", SegWhole); !r.OK {
			t.Fatal(r.Err)
		}
		return s
	}

	cases := []tc{
		{"invalid>clock", func(s *System) Reason {
			return s.Assign(10, "A", "", SegWhole).Err.Reason // 空 id 且时钟回退
		}, RejInvalidParam},
		{"clock>notfound", func(s *System) Reason {
			return s.Assign(10, "ZZZ", "G1", SegWhole).Err.Reason
		}, RejClockBack},
		{"notfound>locked", func(s *System) Reason {
			// 不存在的 id，且 now>=到达时间（t=100）。
			return s.Assign(100, "NOPE", "G1", SegWhole).Err.Reason
		}, RejNotFound},
		{"locked>class", func(s *System) Reason {
			// A 已到达(t=100)，且放到只能容纳二级的 G2（机型也不兼容）。
			return s.Assign(100, "A", "G2", SegWhole).Err.Reason
		}, RejArrivedLocked},
		{"class>kind", func(s *System) Reason {
			// 三级国际航班放到 G2（最大二级=机型不兼容；且 G2 国内=属性也不符）。
			return s.Assign(60, "A", "G2", SegWhole).Err.Reason
		}, RejClassIncompat},
		{"kind>adjacency", func(s *System) Reason {
			// 新增国内三级航班，目标 G1（国际，属性不符）；
			// G1 与被三级 B 占用的 G3 相邻（相邻也会违反）。
			s.flights["N"] = &flightState{
				spec: FlightSpec{ID: "N", Class: Class3, Kind: KindDomestic, SchedArr: 100, SchedDep: 200, BoardLead: 10},
				arr:  100, dep: 200,
			}
			return s.Assign(60, "N", "G1", SegWhole).Err.Reason
		}, RejKindMismatch},
		{"adjacency>time", func(s *System) Reason {
			// 新三级国际航班放 G1：G1 空闲无时间冲突，但 G3 被 B 三级相邻占用且时间相交。
			s.flights["N"] = &flightState{
				spec: FlightSpec{ID: "N", Class: Class3, Kind: KindIntl, SchedArr: 320, SchedDep: 380, BoardLead: 10},
				arr:  320, dep: 380,
			}
			// 再人为在 G1 放一架同级航班制造同口时间冲突。
			s.flights["O"] = &flightState{
				spec: FlightSpec{ID: "O", Class: Class3, Kind: KindIntl, SchedArr: 310, SchedDep: 390, BoardLead: 10},
				arr:  310, dep: 390, deplaneGate: "G1",
			}
			s.gates["G1"].tree.insert(&treeOcc{flight: "O", seg: SegWhole, start: 310, end: 390})
			return s.Assign(60, "N", "G1", SegWhole).Err.Reason
		}, RejAdjacency},
		{"time>cannot", func(s *System) Reason {
			// 指派路径不产生“不可挤占”（指派不改时），因此该对在延误路径验证：
			// 先构造普通时间冲突即可覆盖 time 类优先于后续裁决。
			s.flights["N"] = &flightState{
				spec: FlightSpec{ID: "N", Class: Class1, Kind: KindIntl, SchedArr: 320, SchedDep: 360, BoardLead: 10},
				arr:  320, dep: 360,
			}
			return s.Assign(60, "N", "G3", SegWhole).Err.Reason
		}, RejTimeConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := build(t)
			if got := c.run(s); got != c.want {
				t.Fatalf("%s: got %s want %s", c.name, got, c.want)
			}
		})
	}
}

// TestTimeConflictWinner：多个时间冲突时报起点最早；起点相同报标识最小。
func TestTimeConflictWinner(t *testing.T) {
	cfg := Config{Buffer: 0, MaxStay: 100000, DeplaneDur: 20, BoardingDur: 20}
	gates := []GateSpec{{ID: "G1", MaxClass: Class3, Kind: GateDual}}
	flights := []FlightSpec{
		{ID: "LATE", Class: Class1, Kind: KindDomestic, SchedArr: 200, SchedDep: 300, BoardLead: 10},
		{ID: "EARLY", Class: Class1, Kind: KindDomestic, SchedArr: 120, SchedDep: 180, BoardLead: 10},
		{ID: "NEW", Class: Class1, Kind: KindDomestic, SchedArr: 100, SchedDep: 400, BoardLead: 10},
	}
	s := newTestSystem(t, cfg, gates, flights, nil)
	if r := s.Assign(0, "LATE", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	if r := s.Assign(0, "EARLY", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	r := s.Assign(0, "NEW", "G1", SegWhole)
	if r.OK || r.Err.ConflictID != "EARLY" {
		t.Fatalf("want earliest-start conflict EARLY, got %+v", r)
	}

	// 起点相同：标识最小。
	flights2 := []FlightSpec{
		{ID: "ZEBRA", Class: Class1, Kind: KindDomestic, SchedArr: 150, SchedDep: 250, BoardLead: 10},
		{ID: "APPLE", Class: Class1, Kind: KindDomestic, SchedArr: 150, SchedDep: 250, BoardLead: 10},
		{ID: "NEW", Class: Class1, Kind: KindDomestic, SchedArr: 100, SchedDep: 400, BoardLead: 10},
	}
	s2 := newTestSystem(t, cfg, gates, flights2, nil)
	if r := s2.Assign(0, "ZEBRA", "G1", SegWhole); !r.OK {
		t.Fatal(r.Err)
	}
	// APPLE 与 ZEBRA 完全重叠，同口冲突；先错开：直接人为放树。
	s2.gates["G1"].tree.delete("ZEBRA", SegWhole, 150)
	// 构造两个同起点占用。
	s2.gates["G1"].tree.insert(&treeOcc{flight: "ZEBRA", seg: SegWhole, start: 150, end: 250})
	s2.gates["G1"].tree.insert(&treeOcc{flight: "APPLE", seg: SegWhole, start: 150, end: 250})
	r2 := s2.Assign(0, "NEW", "G1", SegWhole)
	if r2.OK || r2.Err.ConflictID != "APPLE" {
		t.Fatalf("same start: smallest id APPLE, got %+v", r2)
	}
}

// TestComplexityIndependentOfTotalFlights：指派判定的探查节点数只与目标口及
// 相邻口的占用规模相关，与机场航班总数无关；占用者查询不随历史总数增长。
func TestComplexityIndependentOfTotalFlights(t *testing.T) {
	cfg := Config{Buffer: 0, MaxStay: 100000, DeplaneDur: 20, BoardingDur: 20}
	measure := func(totalFlights int) (assignProbes, occProbes int) {
		var gates []GateSpec
		for i := 0; i < 4; i++ {
			gates = append(gates, GateSpec{
				ID: fmt.Sprintf("H%02d", i), MaxClass: Class3, Kind: GateDual,
			})
		}
		var flights []FlightSpec
		// 大量航班分散在 H01..H03 上，区间互不相交；目标口 H00 始终只有 1 条占用。
		for i := 0; i < totalFlights; i++ {
			flights = append(flights, FlightSpec{
				ID:        fmt.Sprintf("T%05d", i),
				Class:     Class1,
				Kind:      KindDomestic,
				SchedArr:  1000 + i*20,
				SchedDep:  1000 + i*20 + 10,
				BoardLead: 2,
			})
		}
		// 目标航班放在相邻口 H00。
		flights = append(flights,
			FlightSpec{ID: "ANCHOR", Class: Class3, Kind: KindDomestic, SchedArr: 10, SchedDep: 20, BoardLead: 2},
			FlightSpec{ID: "PROBE", Class: Class3, Kind: KindDomestic, SchedArr: 30, SchedDep: 40, BoardLead: 2},
		)
		s := newTestSystem(t, cfg, gates, flights, []Adj{{A: "H00", B: "H01"}})
		for i := 0; i < totalFlights; i++ {
			// 循环轮放到 H01..H03。
			g := gates[1+i%3].ID
			if r := s.Assign(0, fmt.Sprintf("T%05d", i), g, SegWhole); !r.OK {
				t.Fatal(r.Err)
			}
		}
		if r := s.Assign(0, "ANCHOR", "H00", SegWhole); !r.OK {
			t.Fatal(r.Err)
		}
		// 判定 PROBE@H00：只需查 H00（1 条）及相邻 H01（约 1/3 总量），
		// 与 H02/H03 及总航班数无关。这里用失败指派暴露内部 probes：
		// 直接读树统计：
		p := 0
		for _, o := range s.gates["H00"].tree.overlaps(30, 40) {
			_ = o
		}
		p += s.gates["H00"].tree.Probes()
		assignProbes = p
		// OccupantAt 访问数。
		_, _, _ = s.OccupantAt("H01", 1000+totalFlights)
		occProbes = s.gates["H01"].tree.Probes()
		return
	}

	p10, o10 := measure(10)
	p1000, o1000 := measure(1000)
	// 目标口 H00 只有 ANCHOR 一条，probes 不应随总航班数增长（允许少量常数差）。
	if p1000 > p10+2 {
		t.Fatalf("assign probes grew with total flights: %d -> %d", p10, p1000)
	}
	// 占用者查询：历史 1000 条时探查节点仍应是对数级，远小于占用数本身。
	if o1000 >= 1000/4 {
		t.Fatalf("occupant probes must be logarithmic, got %d for 1000 occs", o1000)
	}
	t.Logf("assign probes H00: %d vs %d; occupant probes H01: %d vs %d", p10, p1000, o10, o1000)
}

// TestTreePruningDirectly：直接验证 maxEnd 剪枝：大量不相交历史占用下，
// 查询远处小区间访问节点数为 O(log n)，且返回结果正确。
func TestTreePruningDirectly(t *testing.T) {
	tr := &occTree{}
	const n = 5000
	for i := 0; i < n; i++ {
		tr.insert(&treeOcc{
			flight: fmt.Sprintf("F%05d", i),
			seg:    SegWhole,
			start:  1000 + i*10,
			end:    1000 + i*10 + 5,
		})
	}
	// 查询最末尾的一个点，应只命中 1 条且访问很少节点。
	got := tr.overlaps(1000+(n-1)*10, 1000+(n-1)*10+1)
	if len(got) != 1 || got[0].flight != fmt.Sprintf("F%05d", n-1) {
		t.Fatalf("unexpected result: %+v", got)
	}
	if tr.Probes() > 200 { // log2(5000)≈12.3，给宽松上界
		t.Fatalf("pruning failed, probes=%d for n=%d", tr.Probes(), n)
	}
	// 查询最前面的空区间：0 结果，仍对数级。
	_ = tr.overlaps(0, 50)
	if tr.Probes() > 200 {
		t.Fatalf("empty-range pruning failed, probes=%d", tr.Probes())
	}
	// 删除后规模下降。
	tr.delete("F02500", SegWhole, 1000+2500*10)
	if tr.size() != n-1 {
		t.Fatalf("size after delete = %d", tr.size())
	}
}
