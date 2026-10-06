package scheduler

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// 朴素对照模型：与生产实现相互独立，名额靠全量扫描预约计算，
// 用于交叉验证计数器式账本与顺延/释放/改期语义。

type naiveSlot struct {
	start, end int64
	cap        int
}

type naiveRes struct {
	id, region           string
	slotStart, origStart int64
	shifted              bool
	canceled, delivered  bool
	canceledAfterRelease bool
}

type naiveSystem struct {
	cfg     Config
	last    int64
	lastSet bool
	regions map[string][]naiveSlot
	res     map[string]*naiveRes
}

func newNaiveSystem(cfg Config) *naiveSystem {
	return &naiveSystem{cfg: cfg, regions: map[string][]naiveSlot{}, res: map[string]*naiveRes{}}
}

func (n *naiveSystem) checkClock(now int64) error {
	if n.lastSet && now < n.last {
		return newError(CodeClockRollback, "clock rollback")
	}
	return nil
}

func (n *naiveSystem) accept(now int64) { n.last, n.lastSet = now, true }

// occupied 全量扫描所有预约统计已占名额。
func (n *naiveSystem) occupied(regionID string, start int64) int {
	c := 0
	for _, r := range n.res {
		if r.region == regionID && r.slotStart == start && !r.canceled && !r.delivered {
			c++
		}
	}
	return c
}

func (n *naiveSystem) findSlot(regionID string, start int64) *naiveSlot {
	slots := n.regions[regionID]
	for i := range slots {
		if slots[i].start == start {
			return &slots[i]
		}
	}
	return nil
}

func (n *naiveSystem) addRegion(now int64, id string) error {
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.regions[id] = nil
	n.accept(now)
	return nil
}

func (n *naiveSystem) SetCapacity(now int64, regionID string, slotStart int64, cap int) error {
	if cap < 0 {
		return newError(CodeInvalidParam, "cap must be >= 0")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if _, ok := n.regions[regionID]; !ok {
		return newError(CodeRegionNotFound, "region not found")
	}
	sl := n.findSlot(regionID, slotStart)
	if sl == nil {
		return newError(CodeSlotNotFound, "slot not found")
	}
	sl.cap = cap
	n.accept(now)
	return nil
}

func (n *naiveSystem) Place(now int64, resID, regionID string, slotStart int64, acceptShift bool) (PlaceResult, error) {
	if resID == "" || regionID == "" {
		return PlaceResult{}, newError(CodeInvalidParam, "empty id")
	}
	if _, dup := n.res[resID]; dup {
		return PlaceResult{}, newError(CodeInvalidParam, "duplicate reservation id")
	}
	if err := n.checkClock(now); err != nil {
		return PlaceResult{}, err
	}
	slots, ok := n.regions[regionID]
	if !ok {
		return PlaceResult{}, newError(CodeRegionNotFound, "region not found")
	}
	sl := n.findSlot(regionID, slotStart)
	if sl == nil {
		return PlaceResult{}, newError(CodeSlotNotFound, "slot not found")
	}
	if d := sl.start - now; d < n.cfg.EarliestLead {
		return PlaceResult{}, newError(CodeTooEarly, "slot starts too early")
	} else if d > n.cfg.LatestLead {
		return PlaceResult{}, newError(CodeTooLate, "slot starts too late")
	}
	target := *sl
	shifted := false
	if n.occupied(regionID, slotStart) >= sl.cap {
		if !acceptShift {
			return PlaceResult{}, newError(CodeSlotFull, "slot is full")
		}
		found := false
		for _, s := range slots { // slots 已按起点排序，首个命中即最早
			if s.start <= slotStart {
				continue
			}
			if s.start-slotStart > n.cfg.MaxShiftSpan || s.start-now > n.cfg.LatestLead {
				continue
			}
			if n.occupied(regionID, s.start) < s.cap {
				target = s
				found = true
				break
			}
		}
		if !found {
			return PlaceResult{}, newError(CodeSlotFull, "slot is full and no shift candidate")
		}
		shifted = true
	}
	n.res[resID] = &naiveRes{id: resID, region: regionID, slotStart: target.start, origStart: slotStart, shifted: shifted}
	n.accept(now)
	return PlaceResult{RegionID: regionID, SlotStart: target.start, Shifted: shifted, OrigSlotStart: slotStart}, nil
}

func (n *naiveSystem) released(r *naiveRes, now int64) bool {
	return now >= r.slotStart-n.cfg.DispatchLead
}

func (n *naiveSystem) Reschedule(now int64, resID, regionID string, slotStart int64) error {
	if resID == "" || regionID == "" {
		return newError(CodeInvalidParam, "empty id")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	res, ok := n.res[resID]
	if !ok {
		return newError(CodeReservationNotFound, "reservation not found")
	}
	if _, ok := n.regions[regionID]; !ok {
		return newError(CodeRegionNotFound, "region not found")
	}
	ns := n.findSlot(regionID, slotStart)
	if ns == nil {
		return newError(CodeSlotNotFound, "slot not found")
	}
	if res.canceled {
		return newError(CodeAlreadyCanceled, "reservation canceled")
	}
	if res.delivered {
		return newError(CodeAlreadyDelivered, "reservation delivered")
	}
	if n.released(res, now) {
		return newError(CodeAlreadyReleased, "reservation already released")
	}
	if res.region == regionID && res.slotStart == slotStart {
		return newError(CodeNoChange, "already in target slot")
	}
	if now >= res.slotStart-n.cfg.CutoffLead {
		return newError(CodePastCutoff, "past reschedule cutoff")
	}
	if d := ns.start - now; d < n.cfg.EarliestLead {
		return newError(CodeTooEarly, "slot starts too early")
	} else if d > n.cfg.LatestLead {
		return newError(CodeTooLate, "slot starts too late")
	}
	if n.occupied(regionID, slotStart) >= ns.cap {
		return newError(CodeSlotFull, "slot is full")
	}
	res.region = regionID
	res.slotStart = slotStart
	n.accept(now)
	return nil
}

func (n *naiveSystem) Cancel(now int64, resID string) error {
	if resID == "" {
		return newError(CodeInvalidParam, "empty id")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	res, ok := n.res[resID]
	if !ok {
		return newError(CodeReservationNotFound, "reservation not found")
	}
	if res.canceled {
		return newError(CodeAlreadyCanceled, "reservation canceled")
	}
	if res.delivered {
		return newError(CodeAlreadyDelivered, "reservation delivered")
	}
	res.canceled = true
	res.canceledAfterRelease = n.released(res, now)
	n.accept(now)
	return nil
}

func (n *naiveSystem) Deliver(now int64, resID string) error {
	if resID == "" {
		return newError(CodeInvalidParam, "empty id")
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	res, ok := n.res[resID]
	if !ok {
		return newError(CodeReservationNotFound, "reservation not found")
	}
	if res.canceled {
		return newError(CodeAlreadyCanceled, "reservation canceled")
	}
	if res.delivered {
		return newError(CodeAlreadyDelivered, "reservation delivered")
	}
	if !n.released(res, now) {
		return newError(CodeNotReleased, "reservation not released yet")
	}
	res.delivered = true
	n.accept(now)
	return nil
}

func (n *naiveSystem) GetReservation(now int64, resID string) (ReservationView, error) {
	if resID == "" {
		return ReservationView{}, newError(CodeInvalidParam, "empty id")
	}
	if err := n.checkClock(now); err != nil {
		return ReservationView{}, err
	}
	res, ok := n.res[resID]
	if !ok {
		return ReservationView{}, newError(CodeReservationNotFound, "reservation not found")
	}
	return ReservationView{
		ID:                   res.id,
		RegionID:             res.region,
		SlotStart:            res.slotStart,
		Shifted:              res.shifted,
		OrigSlotStart:        res.origStart,
		Released:             n.released(res, now),
		Canceled:             res.canceled,
		CanceledAfterRelease: res.canceledAfterRelease,
		Delivered:            res.delivered,
	}, nil
}

func (n *naiveSystem) ListSlots(now int64, regionID string, from, to int64) ([]SlotView, error) {
	if from > to {
		return nil, newError(CodeInvalidParam, "from must be <= to")
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	slots, ok := n.regions[regionID]
	if !ok {
		return nil, newError(CodeRegionNotFound, "region not found")
	}
	var out []SlotView
	for _, s := range slots {
		if s.end > from && s.start < to {
			occ := n.occupied(regionID, s.start)
			out = append(out, SlotView{Start: s.start, End: s.end, Cap: s.cap, Occupied: occ, Oversold: occ > s.cap})
		}
	}
	return out, nil
}

// sut 生产实现与朴素模型共同满足的接口。
type sut interface {
	Place(now int64, resID, regionID string, slotStart int64, acceptShift bool) (PlaceResult, error)
	Reschedule(now int64, resID, regionID string, slotStart int64) error
	Cancel(now int64, resID string) error
	Deliver(now int64, resID string) error
	GetReservation(now int64, resID string) (ReservationView, error)
	ListSlots(now int64, regionID string, from, to int64) ([]SlotView, error)
	SetCapacity(now int64, regionID string, slotStart int64, cap int) error
}

var _ sut = (*System)(nil)
var _ sut = (*naiveSystem)(nil)

func normSlots(v []SlotView) []SlotView {
	if len(v) == 0 {
		return nil
	}
	return v
}

func compareStep(t *testing.T, step int, desc string, e1, e2 error) {
	t.Helper()
	if CodeOf(e1) != CodeOf(e2) {
		t.Fatalf("step %d %s: real code %d (%v), naive code %d (%v)",
			step, desc, CodeOf(e1), e1, CodeOf(e2), e2)
	}
}

// runScenario 用同一种子生成随机操作序列，同步作用于生产实现与朴素模型，
// 逐步比对错误码与输出，并打印每步输入、输出与判定依据。
// 返回生产实现每步的输出串，供重放一致性校验。
func runScenario(t *testing.T, cfg Config, seed int64, log bool) []string {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	real, err := NewSystem(cfg)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	naive := newNaiveSystem(cfg)

	regionIDs := []string{"R0", "R1", "R2"}
	slotStarts := map[string][]int64{}
	var now int64
	for _, rg := range regionIDs {
		if err := real.AddRegion(now, rg); err != nil {
			t.Fatalf("AddRegion: %v", err)
		}
		if err := naive.addRegion(now, rg); err != nil {
			t.Fatalf("naive addRegion: %v", err)
		}
		for i := 0; i < 5; i++ {
			start := 400 + int64(i)*150
			cap := rng.Intn(4)
			if err := real.AddSlot(now, rg, start, start+100, cap); err != nil {
				t.Fatalf("AddSlot: %v", err)
			}
			if err := naive.addSlot(now, rg, start, start+100, cap); err != nil {
				t.Fatalf("naive addSlot: %v", err)
			}
			slotStarts[rg] = append(slotStarts[rg], start)
		}
	}

	pickRegion := func() string { return regionIDs[rng.Intn(len(regionIDs))] }
	pickSlot := func(rg string) int64 {
		ss := slotStarts[rg]
		return ss[rng.Intn(len(ss))]
	}
	var pool []string
	ghost, counter := 0, 0
	pickRes := func() string {
		if len(pool) > 0 && rng.Intn(5) > 0 {
			return pool[rng.Intn(len(pool))]
		}
		ghost++
		return fmt.Sprintf("ghost-%d", ghost)
	}
	basis := func(rg string) string {
		views, err := real.ListSlots(now, rg, 0, 1<<62)
		if err != nil {
			return fmt.Sprintf("basis unavailable (code=%d)", CodeOf(err))
		}
		return fmt.Sprintf("basis slots=%v", views)
	}

	now = 100
	var outcomes []string
	for step := 0; step < 300; step++ {
		now += int64(rng.Intn(12))
		if rng.Intn(20) == 0 {
			now -= int64(rng.Intn(8) + 1) // 注入时钟回退
		}
		var desc, out, note string
		switch op := rng.Intn(100); {
		case op < 35:
			resID := fmt.Sprintf("res-%d", counter)
			counter++
			rg := pickRegion()
			st := pickSlot(rg)
			sh := rng.Intn(2) == 0
			r1, e1 := real.Place(now, resID, rg, st, sh)
			r2, e2 := naive.Place(now, resID, rg, st, sh)
			compareStep(t, step, "Place", e1, e2)
			if !reflect.DeepEqual(r1, r2) {
				t.Fatalf("step %d Place result: real=%+v naive=%+v", step, r1, r2)
			}
			if e1 == nil {
				pool = append(pool, resID)
			}
			desc = fmt.Sprintf("Place(%s,%s,%d,shift=%v)", resID, rg, st, sh)
			out = fmt.Sprintf("code=%d result=%+v", CodeOf(e1), r1)
			note = basis(rg)
		case op < 50:
			id, rg := pickRes(), pickRegion()
			st := pickSlot(rg)
			e1 := real.Reschedule(now, id, rg, st)
			e2 := naive.Reschedule(now, id, rg, st)
			compareStep(t, step, "Reschedule", e1, e2)
			desc = fmt.Sprintf("Reschedule(%s,%s,%d)", id, rg, st)
			out = fmt.Sprintf("code=%d", CodeOf(e1))
			note = basis(rg)
		case op < 62:
			id := pickRes()
			e1 := real.Cancel(now, id)
			e2 := naive.Cancel(now, id)
			compareStep(t, step, "Cancel", e1, e2)
			desc = fmt.Sprintf("Cancel(%s)", id)
			out = fmt.Sprintf("code=%d", CodeOf(e1))
		case op < 74:
			id := pickRes()
			e1 := real.Deliver(now, id)
			e2 := naive.Deliver(now, id)
			compareStep(t, step, "Deliver", e1, e2)
			desc = fmt.Sprintf("Deliver(%s)", id)
			out = fmt.Sprintf("code=%d", CodeOf(e1))
		case op < 82:
			id := pickRes()
			v1, e1 := real.GetReservation(now, id)
			v2, e2 := naive.GetReservation(now, id)
			compareStep(t, step, "GetReservation", e1, e2)
			if e1 == nil && !reflect.DeepEqual(v1, v2) {
				t.Fatalf("step %d GetReservation: real=%+v naive=%+v", step, v1, v2)
			}
			desc = fmt.Sprintf("GetReservation(%s)", id)
			out = fmt.Sprintf("code=%d view=%+v", CodeOf(e1), v1)
		case op < 90:
			rg := pickRegion()
			from := int64(rng.Intn(1200))
			to := from + int64(rng.Intn(400))
			s1, e1 := real.ListSlots(now, rg, from, to)
			s2, e2 := naive.ListSlots(now, rg, from, to)
			compareStep(t, step, "ListSlots", e1, e2)
			if e1 == nil && !reflect.DeepEqual(normSlots(s1), normSlots(s2)) {
				t.Fatalf("step %d ListSlots: real=%v naive=%v", step, s1, s2)
			}
			desc = fmt.Sprintf("ListSlots(%s,[%d,%d))", rg, from, to)
			out = fmt.Sprintf("code=%d slots=%v", CodeOf(e1), s1)
		default:
			rg := pickRegion()
			st := pickSlot(rg)
			cap := rng.Intn(5)
			e1 := real.SetCapacity(now, rg, st, cap)
			e2 := naive.SetCapacity(now, rg, st, cap)
			compareStep(t, step, "SetCapacity", e1, e2)
			desc = fmt.Sprintf("SetCapacity(%s,%d,%d)", rg, st, cap)
			out = fmt.Sprintf("code=%d", CodeOf(e1))
			note = basis(rg)
		}
		if log {
			t.Logf("step=%03d now=%d %s => %s | %s", step, now, desc, out, note)
		}
		outcomes = append(outcomes, out)

		if step%40 == 39 {
			fullCrossCheck(t, step, real, naive, now, regionIDs, pool)
		}
	}
	return outcomes
}

// fullCrossCheck 周期性全量比对两个实现的所有时段视图与预约视图。
func fullCrossCheck(t *testing.T, step int, real *System, naive *naiveSystem, now int64, regionIDs, pool []string) {
	t.Helper()
	for _, rg := range regionIDs {
		s1, e1 := real.ListSlots(now, rg, 0, 1<<62)
		s2, e2 := naive.ListSlots(now, rg, 0, 1<<62)
		compareStep(t, step, "crosscheck ListSlots", e1, e2)
		if e1 == nil && !reflect.DeepEqual(normSlots(s1), normSlots(s2)) {
			t.Fatalf("step %d crosscheck region %s: real=%v naive=%v", step, rg, s1, s2)
		}
	}
	for _, id := range pool {
		v1, e1 := real.GetReservation(now, id)
		v2, e2 := naive.GetReservation(now, id)
		compareStep(t, step, "crosscheck GetReservation", e1, e2)
		if e1 == nil && !reflect.DeepEqual(v1, v2) {
			t.Fatalf("step %d crosscheck %s: real=%+v naive=%+v", step, id, v1, v2)
		}
	}
}

// 随机操作序列下，生产实现与全量扫描朴素模型逐步一致；
// 同一序列重放两次，落位与顺延记录完全相同。
func TestRandomizedAgainstNaive(t *testing.T) {
	cfg := Config{DispatchLead: 40, CutoffLead: 100, EarliestLead: 50, LatestLead: 800, MaxShiftSpan: 500}
	for seed := int64(1); seed <= 8; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			first := runScenario(t, cfg, seed, true)
			second := runScenario(t, cfg, seed, false)
			if !reflect.DeepEqual(first, second) {
				t.Fatal("replay of the same op sequence diverged")
			}
		})
	}
}

func (n *naiveSystem) addSlot(now int64, regionID string, start, end int64, cap int) error {
	if err := n.checkClock(now); err != nil {
		return err
	}
	slots, ok := n.regions[regionID]
	if !ok {
		return newError(CodeRegionNotFound, "region not found")
	}
	n.regions[regionID] = append(slots, naiveSlot{start: start, end: end, cap: cap})
	sort.Slice(n.regions[regionID], func(i, j int) bool {
		return n.regions[regionID][i].start < n.regions[regionID][j].start
	})
	n.accept(now)
	return nil
}
