package inventory

import (
	"fmt"
	"math/rand"
	"testing"
)

// 朴素对照模型：按题目规则独立实现。占用数通过扫描全部条目直接计算，
// 不做任何惰性归档、索引或缓存，用于与 Engine 在随机操作序列上逐步比对。
type naive struct {
	holdDur uint64
	last    uint64
	nextID  uint64
	segs    map[string]*naiveSeg
	ents    map[uint64]*naiveEnt
}

type naiveSeg struct {
	seats, overbook int
	auth            [3]int
}

type naiveEnt struct {
	state  State
	legs   []Leg
	pax    int
	expiry uint64
}

func newNaive(holdDur uint64) *naive {
	return &naive{holdDur: holdDur, nextID: 1, segs: map[string]*naiveSeg{}, ents: map[uint64]*naiveEnt{}}
}

// occ 时刻 t 的占用 = 已确认出票 + 尚未到期预占（纯函数，扫描全部条目）。
func (n *naive) occ(segID string, class int, t uint64) int {
	sum := 0
	for _, e := range n.ents {
		switch e.state {
		case StateConfirmed:
		case StateHold:
			if e.expiry <= t {
				continue
			}
		default:
			continue
		}
		for _, l := range e.legs {
			if l.Segment == segID && l.Class == class {
				sum += e.pax
			}
		}
	}
	return sum
}

func (n *naive) avail(segID string, class int, t uint64) int {
	s := n.segs[segID]
	sum := 0
	for j := class; j < 3; j++ {
		sum += n.occ(segID, j, t)
	}
	return s.auth[class] - sum
}

func (n *naive) legFits(segID string, class, pax int, t uint64) (bool, string) {
	if a := n.avail(segID, 0, t); a < pax {
		return false, fmt.Sprintf("avail[0]=%d < pax=%d", a, pax)
	}
	for j := 1; j <= class; j++ {
		if a := n.avail(segID, j, t); a <= 0 {
			return false, fmt.Sprintf("avail[%d]=%d <= 0，等级%d不可售", j, a, class)
		}
	}
	if a := n.avail(segID, class, t); a < pax {
		return false, fmt.Sprintf("avail[%d]=%d < pax=%d", class, a, pax)
	}
	return true, "各等级可用数满足"
}

func (n *naive) checkClock(now uint64) *Error {
	if now < n.last {
		return &Error{Kind: ErrClockRollback, Detail: fmt.Sprintf("now=%d < last=%d", now, n.last)}
	}
	return nil
}

func (n *naive) addSegment(now uint64, id string, seats, overbook int, auth [3]int) (*Error, string) {
	if id == "" || seats <= 0 || overbook < 0 {
		return paramErr("航段标识/座位数/超售上限非法"), "参数非法"
	}
	if err := checkAuth(seats, overbook, auth); err != nil {
		return err, "授权量配置非法"
	}
	if _, dup := n.segs[id]; dup {
		return paramErr("航段已存在: " + id), "航段重复"
	}
	if err := n.checkClock(now); err != nil {
		return err, "时钟回退"
	}
	n.segs[id] = &naiveSeg{seats: seats, overbook: overbook, auth: auth}
	n.last = now
	return nil, "接受"
}

func (n *naive) adjustAuth(now uint64, id string, class, v int) (*Error, string) {
	if id == "" || class < 0 || class > 2 || v < 0 {
		return paramErr("参数非法"), "参数非法"
	}
	s, ok := n.segs[id]
	if ok {
		if class > 0 && v > s.auth[class-1] {
			return paramErr("破坏嵌套次序"), "破坏嵌套次序"
		}
		if class < 2 && v < s.auth[class+1] {
			return paramErr("破坏嵌套次序"), "破坏嵌套次序"
		}
		if class == 0 && v > s.seats+s.overbook {
			return paramErr("超过座位数与超售上限之和"), "超过上限"
		}
	}
	if err := n.checkClock(now); err != nil {
		return err, "时钟回退"
	}
	if !ok {
		return &Error{Kind: ErrNotFound, Segment: id}, "航段不存在"
	}
	s.auth[class] = v
	n.last = now
	return nil, "接受"
}

func (n *naive) hold(now uint64, legs []Leg, pax int) (uint64, uint64, *Error, string) {
	if err := checkLegs(legs, pax); err != nil {
		return 0, 0, err, "参数非法"
	}
	if err := n.checkClock(now); err != nil {
		return 0, 0, err, "时钟回退"
	}
	for _, l := range legs {
		if _, ok := n.segs[l.Segment]; !ok {
			return 0, 0, &Error{Kind: ErrNotFound, Segment: l.Segment}, "航段不存在: " + l.Segment
		}
	}
	for _, l := range legs {
		if ok, why := n.legFits(l.Segment, l.Class, pax, now); !ok {
			return 0, 0, &Error{Kind: ErrInsufficientInventory, Segment: l.Segment}, why
		}
	}
	id := n.nextID
	n.nextID++
	expiry := now + n.holdDur
	n.ents[id] = &naiveEnt{state: StateHold, legs: append([]Leg(nil), legs...), pax: pax, expiry: expiry}
	n.last = now
	return id, expiry, nil, fmt.Sprintf("接受，条目=%d 到期=%d", id, expiry)
}

func (n *naive) findEntry(now, id uint64) (*naiveEnt, *Error, string) {
	if id == 0 {
		return nil, paramErr("条目号非法"), "参数非法"
	}
	if err := n.checkClock(now); err != nil {
		return nil, err, "时钟回退"
	}
	ent, ok := n.ents[id]
	if !ok {
		return nil, &Error{Kind: ErrNotFound, EntryID: id}, "条目不存在"
	}
	if ent.state.expires() && now >= ent.expiry {
		return nil, &Error{Kind: ErrHoldExpired, EntryID: id}, fmt.Sprintf("now=%d >= 到期=%d", now, ent.expiry)
	}
	return ent, nil, ""
}

func (n *naive) confirm(now, id uint64) (*Error, string) {
	ent, err, why := n.findEntry(now, id)
	if err != nil {
		return err, why
	}
	if ent.state != StateHold {
		return &Error{Kind: ErrStateConflict, EntryID: id, State: ent.state}, "当前状态: " + ent.state.String()
	}
	ent.state = StateConfirmed
	n.last = now
	return nil, "接受"
}

func (n *naive) cancel(now, id uint64) (*Error, string) {
	ent, err, why := n.findEntry(now, id)
	if err != nil {
		return err, why
	}
	switch ent.state {
	case StateHold:
		ent.state = StateCancelledHold
	case StateConfirmed:
		ent.state = StateCancelledTicket
	default:
		return &Error{Kind: ErrStateConflict, EntryID: id, State: ent.state}, "当前状态: " + ent.state.String()
	}
	n.last = now
	return nil, "接受"
}

func (n *naive) availability(t uint64, segID string, class int) (int, *Error, string) {
	if segID == "" || class < 0 || class > 2 {
		return 0, paramErr("参数非法"), "参数非法"
	}
	if _, ok := n.segs[segID]; !ok {
		return 0, &Error{Kind: ErrNotFound, Segment: segID}, "航段不存在"
	}
	v := n.avail(segID, class, t)
	return v, nil, fmt.Sprintf("avail=%d", v)
}

func (n *naive) sellable(t uint64, segID string, class int) (bool, *Error, string) {
	if segID == "" || class < 0 || class > 2 {
		return false, paramErr("参数非法"), "参数非法"
	}
	if _, ok := n.segs[segID]; !ok {
		return false, &Error{Kind: ErrNotFound, Segment: segID}, "航段不存在"
	}
	for j := 0; j <= class; j++ {
		if a := n.avail(segID, j, t); a <= 0 {
			return false, nil, fmt.Sprintf("avail[%d]=%d <= 0", j, a)
		}
	}
	return true, nil, "各高等级可用数均大于零"
}

func (n *naive) quote(t uint64, legs []Leg, pax int) (bool, string, *Error, string) {
	if err := checkLegs(legs, pax); err != nil {
		return false, "", err, "参数非法"
	}
	for _, l := range legs {
		if _, ok := n.segs[l.Segment]; !ok {
			return false, "", &Error{Kind: ErrNotFound, Segment: l.Segment}, "航段不存在: " + l.Segment
		}
	}
	for _, l := range legs {
		if ok, why := n.legFits(l.Segment, l.Class, pax, t); !ok {
			return false, l.Segment, nil, why
		}
	}
	return true, "", nil, "可售"
}

// ---- 随机操作序列比对 ----

func errStr(err error) string {
	ee, _ := err.(*Error)
	if ee == nil {
		return "OK"
	}
	return ee.Error()
}

func compareErr(t *testing.T, step int, op string, errE, errM error) {
	t.Helper()
	ee, _ := errE.(*Error)
	mm, _ := errM.(*Error)
	if (ee == nil) != (mm == nil) {
		t.Fatalf("step %d %s: engine=%v, model=%v", step, op, errE, errM)
	}
	if ee == nil {
		return
	}
	if ee.Kind != mm.Kind || ee.Segment != mm.Segment || ee.EntryID != mm.EntryID || ee.State != mm.State {
		t.Fatalf("step %d %s: 错误不一致 engine=%+v model=%+v", step, op, ee, mm)
	}
}

func randomLegs(rng *rand.Rand, pool []string) ([]Leg, int) {
	perm := rng.Perm(len(pool))
	n := 1 + rng.Intn(4)
	legs := make([]Leg, 0, n)
	for i := 0; i < n && i < len(perm); i++ {
		legs = append(legs, Leg{Segment: pool[perm[i]], Class: rng.Intn(3)})
	}
	return legs, 1 + rng.Intn(9)
}

// corruptLegs 以多种方式制造参数非法的行程。
func corruptLegs(rng *rand.Rand, legs []Leg, pax int) ([]Leg, int) {
	switch rng.Intn(6) {
	case 0:
		return legs, 0
	case 1:
		return legs, 10
	case 2: // 同一航段出现两次
		if len(legs) > 0 {
			return append(legs, legs[0]), pax
		}
	case 3: // 五个航段
		for len(legs) < 5 {
			legs = append(legs, Leg{Segment: fmt.Sprintf("G%d", len(legs)), Class: 0})
		}
	case 4: // 舱位越界
		if len(legs) > 0 {
			legs[0].Class = 3
		}
	case 5: // 空航段标识
		if len(legs) > 0 {
			legs[0].Segment = ""
		}
	}
	return legs, pax
}

// TestRandomAgainstNaiveModel 用足够多组随机操作序列把 Engine 与朴素对照
// 模型逐步比对，日志打印每步的输入、输出与判定依据（go test -v 可见）。
func TestRandomAgainstNaiveModel(t *testing.T) {
	const sequences = 30
	const steps = 300
	for seq := 0; seq < sequences; seq++ {
		seq := seq
		t.Run(fmt.Sprintf("seq%02d", seq), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(20261006 + seq)))
			dur := uint64(5 + rng.Intn(96))
			eng := NewEngine(dur)
			mdl := newNaive(dur)
			runRandomSequence(t, rng, eng, mdl, steps)
		})
	}
}

func runRandomSequence(t *testing.T, rng *rand.Rand, eng *Engine, mdl *naive, steps int) {
	pool := []string{"A", "B", "C", "D", "E", "F"}
	var cur uint64
	var maxID uint64
	logStep := func(i int, in, outE, outM, why string) {
		t.Logf("step %03d | %s | engine: %s | model: %s | 依据: %s", i, in, outE, outM, why)
	}
	// 初始注册全部航段（随机但合法的嵌套授权）
	for _, id := range pool {
		seats := 1 + rng.Intn(9)
		overbook := rng.Intn(4)
		a0 := rng.Intn(seats + overbook + 1)
		a1 := rng.Intn(a0 + 1)
		a2 := rng.Intn(a1 + 1)
		auth := [3]int{a0, a1, a2}
		errE := eng.AddSegment(0, id, seats, overbook, auth)
		errM, _ := mdl.addSegment(0, id, seats, overbook, auth)
		compareErr(t, 0, "AddSegment", errE, errM)
	}
	for i := 1; i <= steps; i++ {
		now := cur + uint64(rng.Intn(4))
		if rng.Intn(10) == 0 && cur > 0 {
			now = cur - 1 // 10% 概率制造时钟回退
		}
		switch op := rng.Intn(100); {
		case op < 3: // AddSegment：重复注册或新航段，参数可能非法
			id := pool[rng.Intn(len(pool))]
			if rng.Intn(3) == 0 {
				id = fmt.Sprintf("X%d", rng.Intn(3))
			}
			seats := 1 + rng.Intn(9)
			overbook := rng.Intn(3)
			a0 := rng.Intn(seats + overbook + 2)
			a1 := rng.Intn(a0 + 2)
			a2 := rng.Intn(a1 + 1)
			auth := [3]int{a0, a1, a2}
			errE := eng.AddSegment(now, id, seats, overbook, auth)
			errM, why := mdl.addSegment(now, id, seats, overbook, auth)
			compareErr(t, i, "AddSegment", errE, errM)
			logStep(i, fmt.Sprintf("AddSegment(now=%d id=%s seats=%d ob=%d auth=%v)", now, id, seats, overbook, auth), errStr(errE), errStr(errM), why)
			if errE == nil {
				cur = now
				found := false
				for _, s := range pool {
					if s == id {
						found = true
					}
				}
				if !found {
					pool = append(pool, id)
				}
			}
		case op < 30: // Hold
			legs, pax := randomLegs(rng, pool)
			if rng.Intn(7) == 0 {
				legs, pax = corruptLegs(rng, legs, pax)
			}
			idE, expE, errE := eng.Hold(now, legs, pax)
			idM, expM, errM, why := mdl.hold(now, legs, pax)
			compareErr(t, i, "Hold", errE, errM)
			if idE != idM || expE != expM {
				t.Fatalf("step %d Hold: 条目号/到期时刻不一致 engine=(%d,%d) model=(%d,%d)", i, idE, expE, idM, expM)
			}
			logStep(i, fmt.Sprintf("Hold(now=%d legs=%v pax=%d)", now, legs, pax),
				fmt.Sprintf("id=%d expiry=%d %s", idE, expE, errStr(errE)),
				fmt.Sprintf("id=%d expiry=%d %s", idM, expM, errStr(errM)), why)
			if errE == nil {
				cur = now
				maxID = idE
			}
		case op < 45: // Confirm
			var target uint64
			if maxID > 0 && rng.Intn(5) > 0 {
				target = 1 + uint64(rng.Int63n(int64(maxID)))
			} else {
				target = uint64(rng.Intn(int(maxID) + 3))
			}
			errE := eng.Confirm(now, target)
			errM, why := mdl.confirm(now, target)
			compareErr(t, i, "Confirm", errE, errM)
			logStep(i, fmt.Sprintf("Confirm(now=%d id=%d)", now, target), errStr(errE), errStr(errM), why)
			if errE == nil {
				cur = now
			}
		case op < 60: // Cancel
			var target uint64
			if maxID > 0 && rng.Intn(5) > 0 {
				target = 1 + uint64(rng.Int63n(int64(maxID)))
			} else {
				target = uint64(rng.Intn(int(maxID) + 3))
			}
			errE := eng.Cancel(now, target)
			errM, why := mdl.cancel(now, target)
			compareErr(t, i, "Cancel", errE, errM)
			logStep(i, fmt.Sprintf("Cancel(now=%d id=%d)", now, target), errStr(errE), errStr(errM), why)
			if errE == nil {
				cur = now
			}
		case op < 70: // AdjustAuth
			id := pool[rng.Intn(len(pool))]
			class := rng.Intn(3)
			v := rng.Intn(14)
			errE := eng.AdjustAuth(now, id, class, v)
			errM, why := mdl.adjustAuth(now, id, class, v)
			compareErr(t, i, "AdjustAuth", errE, errM)
			logStep(i, fmt.Sprintf("AdjustAuth(now=%d id=%s class=%d v=%d)", now, id, class, v), errStr(errE), errStr(errM), why)
			if errE == nil {
				cur = now
			}
		case op < 80: // Availability 查询（任意时刻，含早于时钟的回溯查询）
			id := pool[rng.Intn(len(pool))]
			class := rng.Intn(3)
			at := uint64(rng.Intn(int(cur) + 4))
			vE, errE := eng.Availability(at, id, class)
			vM, errM, why := mdl.availability(at, id, class)
			compareErr(t, i, "Availability", errE, errM)
			if errE == nil && vE != vM {
				t.Fatalf("step %d Availability(t=%d %s/%d): engine=%d model=%d", i, at, id, class, vE, vM)
			}
			logStep(i, fmt.Sprintf("Availability(t=%d id=%s class=%d)", at, id, class), fmt.Sprintf("%d %s", vE, errStr(errE)), fmt.Sprintf("%d %s", vM, errStr(errM)), why)
		case op < 86: // Sellable 查询
			id := pool[rng.Intn(len(pool))]
			class := rng.Intn(3)
			at := uint64(rng.Intn(int(cur) + 4))
			vE, errE := eng.Sellable(at, id, class)
			vM, errM, why := mdl.sellable(at, id, class)
			compareErr(t, i, "Sellable", errE, errM)
			if errE == nil && vE != vM {
				t.Fatalf("step %d Sellable(t=%d %s/%d): engine=%v model=%v", i, at, id, class, vE, vM)
			}
			logStep(i, fmt.Sprintf("Sellable(t=%d id=%s class=%d)", at, id, class), fmt.Sprintf("%v %s", vE, errStr(errE)), fmt.Sprintf("%v %s", vM, errStr(errM)), why)
		case op < 94: // Quote 行程可售判定
			legs, pax := randomLegs(rng, pool)
			at := uint64(rng.Intn(int(cur) + 4))
			okE, segE, errE := eng.Quote(at, legs, pax)
			okM, segM, errM, why := mdl.quote(at, legs, pax)
			compareErr(t, i, "Quote", errE, errM)
			if errE == nil && (okE != okM || segE != segM) {
				t.Fatalf("step %d Quote: engine=(%v,%s) model=(%v,%s)", i, okE, segE, okM, segM)
			}
			logStep(i, fmt.Sprintf("Quote(t=%d legs=%v pax=%d)", at, legs, pax), fmt.Sprintf("%v %s %s", okE, segE, errStr(errE)), fmt.Sprintf("%v %s %s", okM, segM, errStr(errM)), why)
		case op < 97: // 导出并在新实例中恢复，之后继续用新实例比对
			data, err := eng.ExportJSON()
			if err != nil {
				t.Fatalf("step %d ExportJSON: %v", i, err)
			}
			eng2, err := ImportJSON(data)
			if err != nil {
				t.Fatalf("step %d ImportJSON: %v", i, err)
			}
			for _, id := range pool {
				for class := 0; class < 3; class++ {
					for _, at := range []uint64{0, cur / 2, cur, cur + 5} {
						v1, _ := eng.Availability(at, id, class)
						v2, _ := eng2.Availability(at, id, class)
						if v1 != v2 {
							t.Fatalf("step %d 恢复前后不一致: t=%d %s/%d %d vs %d", i, at, id, class, v1, v2)
						}
					}
				}
			}
			eng = eng2
			logStep(i, "Export/Import", "OK", "OK", "恢复前后查询逐项相等")
		default: // 时钟大步前进，制造批量过期
			cur += 50 + uint64(rng.Intn(100))
			logStep(i, fmt.Sprintf("时钟前进到 %d", cur), "-", "-", "制造批量过期")
		}
	}
}

// ---- 性能证明 ----

// TestPerfCountersProveNoGrowth 用内部工作量计数器证明：
// 查询可用数与执行预占的开销，不随累计预占/出票数量增长，
// 也不随已过期但从未被触及的预占数量增长。
func TestPerfCountersProveNoGrowth(t *testing.T) {
	e := NewEngine(10)
	mustAddSegment(t, e, 0, "A", 30000, 0, [3]int{30000, 30000, 30000})
	mustAddSegment(t, e, 0, "B", 10, 0, [3]int{10, 10, 10})
	// A 上制造 20000 条预占（t=1，到期时刻 11），随后只在 B 上推进时钟，
	// 使这 20000 条全部过期且从未被任何操作触及。
	const N = 20000
	for i := 0; i < N; i++ {
		if _, _, err := e.Hold(1, []Leg{{"A", 0}}, 1); err != nil {
			t.Fatalf("第 %d 条预占失败: %v", i, err)
		}
	}
	mustHold(t, e, 100, []Leg{{"B", 0}}, 1) // 时钟推进到 100

	// 第一次查询触发一次性惰性归档：每条过期预占至多被归档一次（均摊 O(1)）。
	s0 := e.Stats()
	mustAvail(t, e, 100, "A", 0)
	s1 := e.Stats()
	if got := s1.SweptEntries - s0.SweptEntries; got != N {
		t.Fatalf("一次性归档条目数 = %d，期望 %d", got, N)
	}

	// 之后的查询、可售判定与预占：归档数、热表扫描、归档查找全部为零增量，
	// 即开销与累计 20000 条过期预占无关。
	mustAvail(t, e, 100, "A", 0)
	mustAvail(t, e, 50, "A", 0) // 回溯查询：归档区有序，直接二分/短路
	if ok, _ := e.Sellable(100, "A", 0); !ok {
		t.Fatal("过期预占释放后应可售")
	}
	if ok, _, _ := e.Quote(100, []Leg{{"A", 0}, {"B", 0}}, 9); !ok {
		t.Fatal("行程应可售")
	}
	mustHold(t, e, 101, []Leg{{"A", 0}}, 9)
	s2 := e.Stats()
	if s2.SweptEntries != s1.SweptEntries {
		t.Fatalf("再次查询/预占触发了新的归档: %d -> %d", s1.SweptEntries, s2.SweptEntries)
	}
	if s2.HotScanSteps != 0 {
		t.Fatalf("出现了热表扫描: %d 步", s2.HotScanSteps)
	}
	if s2.ArchLookups != 0 {
		t.Fatalf("前向路径出现了归档区二分查找: %d 次", s2.ArchLookups)
	}
}

// BenchmarkAvailabilityExpiredBacklog 在不同规模的已过期未触及预占
// 积压下测量可用数查询：ns/op 应基本平坦（go test -bench 可验证）。
func BenchmarkAvailabilityExpiredBacklog(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("expired=%d", n), func(b *testing.B) {
			e := NewEngine(10)
			seats := n + 10
			_ = e.AddSegment(0, "A", seats, 0, [3]int{seats, seats, seats})
			_ = e.AddSegment(0, "B", 10, 0, [3]int{10, 10, 10})
			for i := 0; i < n; i++ {
				_, _, _ = e.Hold(1, []Leg{{"A", 0}}, 1)
			}
			_, _, _ = e.Hold(100, []Leg{{"B", 0}}, 1) // 推进时钟，积压全部过期
			_, _ = e.Availability(100, "A", 0)        // 一次性归档，不计入计时
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = e.Availability(100, "A", 0)
			}
		})
	}
}

// BenchmarkHoldExpiredBacklog 在不同规模的已过期未触及预占积压下测量预占。
func BenchmarkHoldExpiredBacklog(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("expired=%d", n), func(b *testing.B) {
			e := NewEngine(10)
			seats := n + 20_000_000
			_ = e.AddSegment(0, "A", seats, 0, [3]int{seats, seats, seats})
			_ = e.AddSegment(0, "B", 10, 0, [3]int{10, 10, 10})
			for i := 0; i < n; i++ {
				_, _, _ = e.Hold(1, []Leg{{"A", 0}}, 1)
			}
			_, _, _ = e.Hold(100, []Leg{{"B", 0}}, 1)
			_, _ = e.Availability(100, "A", 0)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _, _ = e.Hold(100, []Leg{{"A", 0}}, 1)
			}
		})
	}
}

// BenchmarkQuoteExpiredBacklog 在不同积压规模下测量行程可售判定。
func BenchmarkQuoteExpiredBacklog(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("expired=%d", n), func(b *testing.B) {
			e := NewEngine(10)
			seats := n + 10
			_ = e.AddSegment(0, "A", seats, 0, [3]int{seats, seats, seats})
			_ = e.AddSegment(0, "B", 10, 0, [3]int{10, 10, 10})
			for i := 0; i < n; i++ {
				_, _, _ = e.Hold(1, []Leg{{"A", 0}}, 1)
			}
			_, _, _ = e.Hold(100, []Leg{{"B", 0}}, 1)
			_, _ = e.Availability(100, "A", 0)
			legs := []Leg{{"A", 0}, {"B", 0}}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _, _ = e.Quote(100, legs, 1)
			}
		})
	}
}
