package flights

import (
	"fmt"
	"sync"
	"testing"
)

func mustEngine(t *testing.T, flights []Flight, cfg Config) *Engine {
	t.Helper()
	e, err := NewEngine(flights, cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustQuery(t *testing.T, e *Engine, id string) Result {
	t.Helper()
	r, err := e.Query(id)
	if err != nil {
		t.Fatalf("Query(%s): %v", id, err)
	}
	return r
}

func baseCfg() Config {
	return Config{MinTurnaround: 10, MinConnection: 10, MaxDuty: 100000, Curfews: map[string]Curfew{}}
}

// 起飞时刻恰落在宵禁起点（推迟到终点）与恰等于宵禁终点（不推迟）的区别。
func TestCurfewDepartureBoundary(t *testing.T) {
	cfg := baseCfg()
	cfg.Curfews["A"] = Curfew{Start: 100, End: 200}
	flights := []Flight{
		{ID: "F1", SchedDep: 100, Duration: 10, Origin: "A", Dest: "B", Aircraft: "AC1", Crew: "K1"},
		{ID: "F2", SchedDep: 200, Duration: 10, Origin: "A", Dest: "B", Aircraft: "AC2", Crew: "K2"},
		{ID: "F3", SchedDep: 99, Duration: 10, Origin: "A", Dest: "B", Aircraft: "AC3", Crew: "K3"},
		{ID: "F4", SchedDep: 150, Duration: 10, Origin: "A", Dest: "B", Aircraft: "AC4", Crew: "K4"},
	}
	e := mustEngine(t, flights, cfg)
	if r := mustQuery(t, e, "F1"); r.Dep != 200 || r.Status != StatusDelayed {
		t.Fatalf("F1 起飞恰在宵禁起点应推迟到终点: %+v", r)
	}
	if r := mustQuery(t, e, "F2"); r.Dep != 200 || r.Status != StatusOnTime {
		t.Fatalf("F2 起飞恰等于宵禁终点不应推迟: %+v", r)
	}
	if r := mustQuery(t, e, "F3"); r.Dep != 99 || r.Status != StatusOnTime {
		t.Fatalf("F3 起飞早于宵禁起点不应推迟: %+v", r)
	}
	if r := mustQuery(t, e, "F4"); r.Dep != 200 || r.Status != StatusDelayed {
		t.Fatalf("F4 起飞落入宵禁应推迟到终点: %+v", r)
	}
}

// 到达时刻恰落在宵禁起点（取消）与恰等于宵禁终点（正常）的区别。
func TestCurfewArrivalBoundary(t *testing.T) {
	cfg := baseCfg()
	cfg.Curfews["B"] = Curfew{Start: 350, End: 400}
	flights := []Flight{
		{ID: "F1", SchedDep: 300, Duration: 50, Origin: "A", Dest: "B", Aircraft: "AC1", Crew: "K1"},
		{ID: "F2", SchedDep: 300, Duration: 100, Origin: "A", Dest: "B", Aircraft: "AC2", Crew: "K2"},
		{ID: "F3", SchedDep: 300, Duration: 49, Origin: "A", Dest: "B", Aircraft: "AC3", Crew: "K3"},
	}
	e := mustEngine(t, flights, cfg)
	if r := mustQuery(t, e, "F1"); r.Status != StatusCancelled || r.Reason != ReasonCurfew {
		t.Fatalf("F1 到达恰在宵禁起点应取消(宵禁): %+v", r)
	}
	if r := mustQuery(t, e, "F2"); r.Status != StatusOnTime || r.Arr != 400 {
		t.Fatalf("F2 到达恰等于宵禁终点应正常: %+v", r)
	}
	if r := mustQuery(t, e, "F3"); r.Status != StatusOnTime || r.Arr != 349 {
		t.Fatalf("F3 到达早于宵禁起点应正常: %+v", r)
	}
}

// 到达落入宵禁触发取消，并沿飞机链与机组链各传导至少两段。
func TestArrivalCurfewCancellationPropagation(t *testing.T) {
	cfg := baseCfg()
	cfg.Curfews["B"] = Curfew{Start: 150, End: 250}
	flights := []Flight{
		{ID: "F1", SchedDep: 100, Duration: 60, Origin: "A", Dest: "B", Aircraft: "AC1", Crew: "K1"},
		{ID: "F2", SchedDep: 300, Duration: 50, Origin: "B", Dest: "C", Aircraft: "AC1", Crew: "K2"},
		{ID: "F3", SchedDep: 400, Duration: 50, Origin: "C", Dest: "A", Aircraft: "AC1", Crew: "K2"},
		{ID: "F4", SchedDep: 500, Duration: 50, Origin: "A", Dest: "D", Aircraft: "AC1", Crew: "K2"},
		{ID: "F5", SchedDep: 600, Duration: 50, Origin: "D", Dest: "E", Aircraft: "AC2", Crew: "K2"},
	}
	e := mustEngine(t, flights, cfg)
	if r := mustQuery(t, e, "F1"); r.Status != StatusCancelled || r.Reason != ReasonCurfew {
		t.Fatalf("F1 到达落入宵禁应取消(宵禁): %+v", r)
	}
	// 飞机 AC1 停留在 A：F2(B)、F3(C) 无飞机；F4 从 A 起飞，飞机侧传导停止。
	if r := mustQuery(t, e, "F2"); r.Status != StatusCancelled || r.Reason != ReasonNoAircraft {
		t.Fatalf("F2 应无飞机取消: %+v", r)
	}
	if r := mustQuery(t, e, "F3"); r.Status != StatusCancelled || r.Reason != ReasonNoAircraft {
		t.Fatalf("F3 应无飞机取消: %+v", r)
	}
	// 机组 K2 停留在 B：F4(A)、F5(D) 无机组，沿机组链传导两段。
	if r := mustQuery(t, e, "F4"); r.Status != StatusCancelled || r.Reason != ReasonNoCrew {
		t.Fatalf("F4 应无机组取消: %+v", r)
	}
	if r := mustQuery(t, e, "F5"); r.Status != StatusCancelled || r.Reason != ReasonNoCrew {
		t.Fatalf("F5 应无机组取消: %+v", r)
	}
}

// 取消后飞机所在机场恰等于后续某段起飞机场时，传导停止。
func TestPropagationStopsWhenLocationMatches(t *testing.T) {
	flights := []Flight{
		{ID: "F1", SchedDep: 100, Duration: 50, Origin: "A", Dest: "B", Aircraft: "AC1", Crew: "K1"},
		{ID: "F2", SchedDep: 200, Duration: 50, Origin: "B", Dest: "A", Aircraft: "AC1", Crew: "K2"},
		{ID: "F3", SchedDep: 300, Duration: 50, Origin: "A", Dest: "C", Aircraft: "AC1", Crew: "K3"},
	}
	e := mustEngine(t, flights, baseCfg())
	if _, err := e.InjectCancel("F1", 0); err != nil {
		t.Fatalf("InjectCancel: %v", err)
	}
	if r := mustQuery(t, e, "F2"); r.Status != StatusCancelled || r.Reason != ReasonNoAircraft {
		t.Fatalf("F2 应无飞机取消: %+v", r)
	}
	// 飞机停留在 A，F3 恰从 A 起飞，传导停止，正常执行。
	if r := mustQuery(t, e, "F3"); r.Status != StatusOnTime || r.Dep != 300 {
		t.Fatalf("F3 应正常执行: %+v", r)
	}
}

// 机组跨度恰等于上限（允许）与多一分钟（取消）。
func TestDutyBoundaryExact(t *testing.T) {
	flights := []Flight{
		{ID: "F1", SchedDep: 0, Duration: 300, Origin: "A", Dest: "B", Aircraft: "AC1", Crew: "K1"},
		{ID: "F2", SchedDep: 400, Duration: 300, Origin: "B", Dest: "C", Aircraft: "AC2", Crew: "K1"},
		{ID: "F3", SchedDep: 800, Duration: 50, Origin: "C", Dest: "D", Aircraft: "AC3", Crew: "K1"},
	}
	// 跨度 700-0=700 恰等于上限：允许。
	cfgOK := baseCfg()
	cfgOK.MaxDuty = 700
	e := mustEngine(t, flights, cfgOK)
	if r := mustQuery(t, e, "F2"); r.Status == StatusCancelled || r.Dep != 400 || r.Arr != 700 {
		t.Fatalf("F2 跨度恰等于上限应允许: %+v", r)
	}
	// 上限 699：F2 超限取消，F3 沿机组链后缀传导取消。
	cfgOver := baseCfg()
	cfgOver.MaxDuty = 699
	e2 := mustEngine(t, flights, cfgOver)
	if r := mustQuery(t, e2, "F2"); r.Status != StatusCancelled || r.Reason != ReasonDuty {
		t.Fatalf("F2 跨度超限应取消(值勤超限): %+v", r)
	}
	if r := mustQuery(t, e2, "F3"); r.Status != StatusCancelled || r.Reason != ReasonDuty {
		t.Fatalf("F3 应沿机组链传导取消(值勤超限): %+v", r)
	}
}

// 交叉链测试航班表：F1 的飞机后继是 F2、机组后继是 F3，两条链相互交叉。
func crossingFlights() []Flight {
	return []Flight{
		{ID: "F1", SchedDep: 100, Duration: 50, Origin: "A", Dest: "B", Aircraft: "AC1", Crew: "K1"},
		{ID: "F2", SchedDep: 300, Duration: 50, Origin: "B", Dest: "C", Aircraft: "AC1", Crew: "K2"},
		{ID: "F3", SchedDep: 310, Duration: 50, Origin: "B", Dest: "C", Aircraft: "AC2", Crew: "K1"},
		{ID: "F4", SchedDep: 500, Duration: 50, Origin: "C", Dest: "A", Aircraft: "AC2", Crew: "K2"},
	}
}

// 飞机链与机组链相互交叉时，结论与注入次序无关。
func TestInjectionOrderIndependence(t *testing.T) {
	type op struct {
		kind    string
		flight  string
		minutes int
	}
	ops := []op{
		{kind: "delay", flight: "F1", minutes: 200},
		{kind: "cancel", flight: "F3"},
		{kind: "delay", flight: "F2", minutes: 30},
	}
	apply := func(e *Engine, seq []int) {
		t.Helper()
		for _, i := range seq {
			o := ops[i]
			var err error
			if o.kind == "delay" {
				_, err = e.InjectDelay(o.flight, o.minutes, 0)
			} else {
				_, err = e.InjectCancel(o.flight, 0)
			}
			if err != nil {
				t.Fatalf("op %+v: %v", o, err)
			}
		}
	}
	e1 := mustEngine(t, crossingFlights(), baseCfg())
	apply(e1, []int{0, 1, 2})
	e2 := mustEngine(t, crossingFlights(), baseCfg())
	apply(e2, []int{2, 0, 1})
	e3 := mustEngine(t, crossingFlights(), baseCfg())
	apply(e3, []int{1, 2, 0})
	for _, id := range []string{"F1", "F2", "F3", "F4"} {
		r1 := mustQuery(t, e1, id)
		r2 := mustQuery(t, e2, id)
		r3 := mustQuery(t, e3, id)
		if r1 != r2 || r1 != r3 {
			t.Fatalf("%s 注入次序不同导致结论不同: %+v / %+v / %+v", id, r1, r2, r3)
		}
	}
}

// 撤回后结论等于从未注入过。
func TestWithdrawRestores(t *testing.T) {
	e := mustEngine(t, crossingFlights(), baseCfg())
	snapshot := map[string]Result{}
	for _, id := range []string{"F1", "F2", "F3", "F4"} {
		snapshot[id] = mustQuery(t, e, id)
	}
	d1, err := e.InjectDelay("F1", 200, 0)
	if err != nil {
		t.Fatalf("InjectDelay: %v", err)
	}
	c1, err := e.InjectCancel("F3", 0)
	if err != nil {
		t.Fatalf("InjectCancel: %v", err)
	}
	if r := mustQuery(t, e, "F2"); r == snapshot["F2"] {
		t.Fatalf("注入后 F2 结论应发生变化: %+v", r)
	}
	if err := e.Withdraw("F1", d1, 0); err != nil {
		t.Fatalf("Withdraw delay: %v", err)
	}
	if err := e.Withdraw("F3", c1, 0); err != nil {
		t.Fatalf("Withdraw cancel: %v", err)
	}
	for id, want := range snapshot {
		if got := mustQuery(t, e, id); got != want {
			t.Fatalf("%s 撤回后应恢复原状: got %+v want %+v", id, got, want)
		}
	}
}

// 已实际起飞的航班不可再注入或撤回，其实际时刻固定不再随上游变化。
func TestFrozenFlightFixed(t *testing.T) {
	flights := []Flight{
		{ID: "F0", SchedDep: 100, Duration: 250, Origin: "A", Dest: "A", Aircraft: "AC1", Crew: "K1"},
		{ID: "F1", SchedDep: 300, Duration: 50, Origin: "A", Dest: "C", Aircraft: "AC1", Crew: "K2"},
	}
	e := mustEngine(t, flights, baseCfg())
	if r := mustQuery(t, e, "F1"); r.Dep != 360 {
		t.Fatalf("F1 初始应受前段过站约束推迟到 360: %+v", r)
	}
	id, err := e.InjectCancel("F0", 50)
	if err != nil {
		t.Fatalf("InjectCancel: %v", err)
	}
	if r := mustQuery(t, e, "F1"); r.Dep != 300 {
		t.Fatalf("F0 取消后 F1 不再受过站约束: %+v", r)
	}
	// F1 实际起飞 300 <= 400，已起飞不可改。
	if _, err := e.InjectDelay("F1", 10, 400); err != ErrAlreadyDeparted {
		t.Fatalf("已起飞航班注入应拒绝: %v", err)
	}
	// 撤回 F0 的取消后，F1 已冻结，实际时刻固定为 300，不随上游变化。
	if err := e.Withdraw("F0", id, 400); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if r := mustQuery(t, e, "F1"); r.Dep != 300 {
		t.Fatalf("F1 已冻结，时刻应固定为 300: %+v", r)
	}
	if r := mustQuery(t, e, "F0"); r.Dep != 100 {
		t.Fatalf("F0 撤回取消后恢复起飞 100: %+v", r)
	}
	// F0 实际起飞 100 <= 500，同样不可再注入。
	if _, err := e.InjectDelay("F0", 10, 500); err != ErrAlreadyDeparted {
		t.Fatalf("已起飞航班注入应拒绝: %v", err)
	}
}

// 拒绝次序：参数非法 > 时钟回退 > 航班不存在 > 已起飞不可改 > 注入记录不存在。
func TestRejectionOrder(t *testing.T) {
	flights := []Flight{
		{ID: "F1", SchedDep: 100, Duration: 50, Origin: "A", Dest: "B", Aircraft: "AC1", Crew: "K1"},
		{ID: "F2", SchedDep: 3000, Duration: 50, Origin: "B", Dest: "C", Aircraft: "AC1", Crew: "K1"},
	}
	e := mustEngine(t, flights, baseCfg())
	// 负延误 + 负时刻 + 航班不存在：报参数非法。
	if _, err := e.InjectDelay("NOPE", -5, -1); err != ErrInvalidParam {
		t.Fatalf("参数非法应最先报: %v", err)
	}
	if _, err := e.InjectDelay("F1", 10, 50); err != nil {
		t.Fatalf("InjectDelay: %v", err)
	}
	// 时钟回退 + 航班不存在：报时钟回退。
	if _, err := e.InjectDelay("NOPE", 10, 49); err != ErrClockRollback {
		t.Fatalf("时钟回退应优先于航班不存在: %v", err)
	}
	if _, err := e.InjectDelay("NOPE", 10, 50); err != ErrFlightNotFound {
		t.Fatalf("应报航班不存在: %v", err)
	}
	if _, err := e.InjectDelay("F2", 5, 1000); err != nil {
		t.Fatalf("InjectDelay: %v", err)
	}
	// F1 实际起飞 110 <= 1000 已起飞：已起飞不可改优先于注入记录不存在。
	if err := e.Withdraw("F1", 999, 1000); err != ErrAlreadyDeparted {
		t.Fatalf("已起飞不可改应优先于注入记录不存在: %v", err)
	}
	if err := e.Withdraw("F2", 999, 1000); err != ErrInjectionNotFound {
		t.Fatalf("应报注入记录不存在: %v", err)
	}
	if err := e.Withdraw("NOPE", 1, 1000); err != ErrFlightNotFound {
		t.Fatalf("应报航班不存在: %v", err)
	}
}

// 被拒绝的操作不推进时钟、不改任何注入记录。
func TestRejectedOpsHaveNoEffect(t *testing.T) {
	flights := []Flight{
		{ID: "F1", SchedDep: 100, Duration: 50, Origin: "A", Dest: "B", Aircraft: "AC1", Crew: "K1"},
	}
	flights[0].SchedDep = 200
	e := mustEngine(t, flights, baseCfg())
	id1, err := e.InjectDelay("F1", 10, 100)
	if err != nil {
		t.Fatalf("InjectDelay: %v", err)
	}
	// 时钟回退被拒绝后，时钟仍停留在 100。
	if _, err := e.InjectDelay("F1", 10, 99); err != ErrClockRollback {
		t.Fatalf("应报时钟回退: %v", err)
	}
	if _, err := e.InjectDelay("F1", 5, 100); err != nil {
		t.Fatalf("被拒绝的操作不应推进时钟: %v", err)
	}
	if r := mustQuery(t, e, "F1"); r.Dep != 215 {
		t.Fatalf("两条延误叠加后起飞应为 215: %+v", r)
	}
	// 撤回不存在的记录被拒绝，不改任何注入记录。
	if err := e.Withdraw("F1", 999, 100); err != ErrInjectionNotFound {
		t.Fatalf("应报注入记录不存在: %v", err)
	}
	if err := e.Withdraw("F1", id1, 100); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if r := mustQuery(t, e, "F1"); r.Dep != 205 {
		t.Fatalf("撤回一条延误后起飞应为 205: %+v", r)
	}
	// 同一记录不可重复撤回。
	if err := e.Withdraw("F1", id1, 100); err != ErrInjectionNotFound {
		t.Fatalf("重复撤回应报注入记录不存在: %v", err)
	}
}

// 航班表参数非法的各种情形。
func TestInvalidSchedule(t *testing.T) {
	valid := []Flight{
		{ID: "F1", SchedDep: 100, Duration: 50, Origin: "A", Dest: "B", Aircraft: "AC1", Crew: "K1"},
		{ID: "F2", SchedDep: 200, Duration: 50, Origin: "B", Dest: "C", Aircraft: "AC1", Crew: "K1"},
	}
	cases := map[string]struct {
		mutate func(fs []Flight) []Flight
		cfg    func(c Config) Config
	}{
		"飞机链机场不衔接": {mutate: func(fs []Flight) []Flight { fs[1].Origin = "X"; return fs }},
		"机组链机场不衔接": {mutate: func(fs []Flight) []Flight {
			fs[1].Aircraft = "AC2"
			fs[1].Origin = "X"
			return fs
		}},
		"链内计划起飞时刻相同": {mutate: func(fs []Flight) []Flight { fs[1].SchedDep = 100; return fs }},
		"负飞行时长":      {mutate: func(fs []Flight) []Flight { fs[0].Duration = -1; return fs }},
		"负计划起飞时刻":    {mutate: func(fs []Flight) []Flight { fs[0].SchedDep = -1; return fs }},
		"航班ID重复": {mutate: func(fs []Flight) []Flight {
			fs[1].ID = "F1"
			fs[1].Aircraft = "AC2"
			fs[1].Crew = "K2"
			return fs
		}},
		"航班ID为空": {mutate: func(fs []Flight) []Flight { fs[0].ID = ""; return fs }},
		"宵禁区间非法": {cfg: func(c Config) Config {
			c.Curfews["A"] = Curfew{Start: 300, End: 200}
			return c
		}},
		"负过站时间": {cfg: func(c Config) Config { c.MinTurnaround = -1; return c }},
	}
	for name, tc := range cases {
		fs := append([]Flight(nil), valid...)
		cfg := baseCfg()
		if tc.mutate != nil {
			fs = tc.mutate(fs)
		}
		if tc.cfg != nil {
			cfg = tc.cfg(cfg)
		}
		if _, err := NewEngine(fs, cfg); err != ErrInvalidParam {
			t.Fatalf("%s: 应报参数非法, got %v", name, err)
		}
	}
}

// 增量重算开销不随未受影响航班数量增长（用重算计数器验证）。
func TestRecomputeCostLocality(t *testing.T) {
	build := func(extra int) *Engine {
		flights := make([]Flight, 0, 50+extra)
		for i := 0; i < 50; i++ {
			origin, dest := "A", "B"
			if i%2 == 1 {
				origin, dest = "B", "A"
			}
			flights = append(flights, Flight{
				ID: fmt.Sprintf("M%02d", i), SchedDep: 100 + i*200, Duration: 50,
				Origin: origin, Dest: dest, Aircraft: "AC0", Crew: fmt.Sprintf("K%02d", i),
			})
		}
		for j := 0; j < extra; j++ {
			flights = append(flights, Flight{
				ID: fmt.Sprintf("X%04d", j), SchedDep: 50, Duration: 20,
				Origin: "P", Dest: "Q",
				Aircraft: fmt.Sprintf("AX%04d", j), Crew: fmt.Sprintf("KX%04d", j),
			})
		}
		return mustEngine(t, flights, baseCfg())
	}
	e1, e2 := build(100), build(3000)
	// 小延误被过站裕量吸收：仅波及注入航班本身及其直接后继的检查。
	if _, err := e1.InjectDelay("M25", 5, 0); err != nil {
		t.Fatalf("InjectDelay: %v", err)
	}
	if _, err := e2.InjectDelay("M25", 5, 0); err != nil {
		t.Fatalf("InjectDelay: %v", err)
	}
	if c1, c2 := e1.LastRecomputeCost(), e2.LastRecomputeCost(); c1 != c2 || c1 > 4 {
		t.Fatalf("局部变更的重算开销不应随无关航班数增长: %d vs %d", c1, c2)
	}
	// 大延误沿主链传导：开销正比于受影响航段数，同样与无关航班数无关。
	if _, err := e1.InjectDelay("M00", 100000, 0); err != nil {
		t.Fatalf("InjectDelay: %v", err)
	}
	if _, err := e2.InjectDelay("M00", 100000, 0); err != nil {
		t.Fatalf("InjectDelay: %v", err)
	}
	if c1, c2 := e1.LastRecomputeCost(), e2.LastRecomputeCost(); c1 != c2 || c1 != 50 {
		t.Fatalf("主链传导的重算开销应为受影响航段数 50: %d vs %d", c1, c2)
	}
	if r1, r2 := mustQuery(t, e1, "M49"), mustQuery(t, e2, "M49"); r1 != r2 {
		t.Fatalf("两引擎结论应一致: %+v vs %+v", r1, r2)
	}
}

// 并发调用等价于某个串行顺序：与串行重放同一注入集合结论一致。
func TestConcurrentEquivalentToSerial(t *testing.T) {
	airports := []string{"A", "B", "C", "D"}
	var flights []Flight
	delays := map[string]int{}
	for a := 0; a < 4; a++ {
		for i := 0; i < 5; i++ {
			id := fmt.Sprintf("F%d_%d", a, i)
			flights = append(flights, Flight{
				ID: id, SchedDep: 100 + i*150 + a*10, Duration: 60,
				Origin: airports[(a+i)%4], Dest: airports[(a+i+1)%4],
				Aircraft: fmt.Sprintf("AC%d", a), Crew: id,
			})
			delays[id] = (a*5 + i*37) % 120
		}
	}
	concurrent := mustEngine(t, flights, baseCfg())
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for idx, f := range flights {
				if idx%8 != worker {
					continue
				}
				if _, err := concurrent.InjectDelay(f.ID, delays[f.ID], 0); err != nil {
					t.Errorf("InjectDelay(%s): %v", f.ID, err)
				}
				if _, err := concurrent.Query(f.ID); err != nil {
					t.Errorf("Query(%s): %v", f.ID, err)
				}
			}
		}(w)
	}
	wg.Wait()
	serial := mustEngine(t, flights, baseCfg())
	for _, f := range flights {
		if _, err := serial.InjectDelay(f.ID, delays[f.ID], 0); err != nil {
			t.Fatalf("serial InjectDelay(%s): %v", f.ID, err)
		}
	}
	for _, f := range flights {
		rc, rs := mustQuery(t, concurrent, f.ID), mustQuery(t, serial, f.ID)
		if rc != rs {
			t.Fatalf("%s 并发与串行结论不一致: %+v vs %+v", f.ID, rc, rs)
		}
	}
}

// 查询单个航班结论的开销与航班总数无关（map 查找）。
func BenchmarkQuery(b *testing.B) {
	var flights []Flight
	for i := 0; i < 5000; i++ {
		origin, dest := "A", "B"
		if i%2 == 1 {
			origin, dest = "B", "A"
		}
		flights = append(flights, Flight{
			ID: fmt.Sprintf("M%04d", i), SchedDep: 100 + i*200, Duration: 50,
			Origin: origin, Dest: dest, Aircraft: "AC0", Crew: fmt.Sprintf("K%04d", i),
		})
	}
	e, err := NewEngine(flights, baseCfg())
	if err != nil {
		b.Fatalf("NewEngine: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Query("M2500"); err != nil {
			b.Fatalf("Query: %v", err)
		}
	}
}
