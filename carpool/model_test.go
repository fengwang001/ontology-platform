package carpool

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 本文件包含一个独立编写的朴素全量重算模型（naiveModel），与 Service
// 对照随机操作序列。naiveModel 把全部历史订单保存在一个切片中从不归档，
// 每次费用计算都扫描全部历史（全量重算）；Service 只遍历在途订单。
// 两者对同一操作序列的输出必须完全一致。

type nOrder struct {
	id              string
	pickup, dropoff int64
	persons         int
	maxDelay        int64
	latest          int64
	submit          int64
	seq             int
	status          OrderStatus
	vid             string
	cap, settled    int64
}

type nVehicle struct {
	id    string
	seats int
	pos   int64
}

type naiveModel struct {
	cfg      Config
	now      int64
	seq      int
	vehicles []*nVehicle // 按字典序
	orders   []*nOrder   // 全部历史，从不移除
}

func newNaive(cfg Config) *naiveModel { return &naiveModel{cfg: cfg} }

func (m *naiveModel) addVehicle(id string, seats int, pos int64) {
	m.vehicles = append(m.vehicles, &nVehicle{id: id, seats: seats, pos: pos})
	sort.Slice(m.vehicles, func(i, j int) bool { return m.vehicles[i].id < m.vehicles[j].id })
	m.expire()
}

func (m *naiveModel) findVehicle(id string) *nVehicle {
	for _, v := range m.vehicles {
		if v.id == id {
			return v
		}
	}
	return nil
}

func (m *naiveModel) findOrder(id string) *nOrder {
	for _, o := range m.orders {
		if o.id == id {
			return o
		}
	}
	return nil
}

// activeOf 全量扫描历史，找出车辆的在途订单。
func (m *naiveModel) activeOf(vid string) []*nOrder {
	var out []*nOrder
	for _, o := range m.orders {
		if o.vid == vid && (o.status == StatusMatched || o.status == StatusOnboard) {
			out = append(out, o)
		}
	}
	return out
}

func nStops(orders []*nOrder) map[int64]bool {
	stops := map[int64]bool{}
	for _, o := range orders {
		stops[o.pickup] = true
		stops[o.dropoff] = true
	}
	return stops
}

func nDelay(o *nOrder, stops map[int64]bool, stopDuration int64) int64 {
	var n int64
	for x := range stops {
		if x > o.pickup && x < o.dropoff {
			n++
		}
	}
	return n * stopDuration
}

// insertDelta 判定订单能否并入车辆，可行时返回在途乘客停靠延误增量。
func (m *naiveModel) insertDelta(v *nVehicle, o *nOrder, now int64) (int64, bool, string) {
	if o.pickup < v.pos {
		return 0, false, "上车点落后于车辆位置"
	}
	active := m.activeOf(v.id)
	if len(active) >= m.cfg.MaxActiveOrders {
		return 0, false, "在途订单数达上限"
	}
	curStops := nStops(active)
	all := append(append([]*nOrder{}, active...), o)
	newStops := nStops(all)
	var delta int64
	for _, a := range active {
		after := nDelay(a, newStops, m.cfg.StopDuration)
		if after > a.maxDelay {
			return 0, false, "在途乘客" + a.id + "停靠延误超限"
		}
		delta += after - nDelay(a, curStops, m.cfg.StopDuration)
	}
	if nDelay(o, newStops, m.cfg.StopDuration) > o.maxDelay {
		return 0, false, "新乘客停靠延误超限"
	}
	pts := map[int64]bool{v.pos: true}
	for x := range newStops {
		pts[x] = true
	}
	var sorted []int64
	for x := range pts {
		sorted = append(sorted, x)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for i := 0; i+1 < len(sorted); i++ {
		var occ int64
		for _, a := range all {
			if a.pickup <= sorted[i] && a.dropoff > sorted[i] {
				occ += int64(a.persons)
			}
		}
		if occ > int64(v.seats) {
			return 0, false, "座位不足"
		}
	}
	est := now + (o.pickup-v.pos)*m.cfg.TimePerDistance
	for x := range newStops {
		if x > v.pos && x < o.pickup {
			est += m.cfg.StopDuration
		}
	}
	if est > o.latest {
		return 0, false, fmt.Sprintf("预计上车时刻%d超过最晚%d", est, o.latest)
	}
	return delta, true, ""
}

// tryMatch 按（延误增量最小，车辆标识字典序最小）选车，并给出判定依据。
func (m *naiveModel) tryMatch(o *nOrder, now int64) (string, bool, string) {
	best := ""
	var bestDelta int64
	found := false
	var why []string
	for _, v := range m.vehicles {
		d, ok, reason := m.insertDelta(v, o, now)
		if !ok {
			why = append(why, v.id+":否("+reason+")")
			continue
		}
		why = append(why, fmt.Sprintf("%s:可(Δ=%d)", v.id, d))
		if !found || d < bestDelta {
			best, bestDelta, found = v.id, d, true
		}
	}
	return best, found, strings.Join(why, " ")
}

// rawFare 全量重算 target 在其车辆当前在途乘客下的分摊金额。
func (m *naiveModel) rawFare(v *nVehicle, target *nOrder) int64 {
	active := m.activeOf(v.id)
	stops := nStops(active)
	var locs []int64
	for x := range stops {
		locs = append(locs, x)
	}
	sort.Slice(locs, func(i, j int) bool { return locs[i] < locs[j] })
	var total int64
	for i := 0; i+1 < len(locs); i++ {
		lo, hi := locs[i], locs[i+1]
		if target.pickup > lo || target.dropoff < hi {
			continue
		}
		base := (hi - lo) * m.cfg.UnitPrice
		var persons int64
		var earliest *nOrder
		for _, o := range active {
			if o.pickup <= lo && o.dropoff >= hi {
				persons += int64(o.persons)
				if earliest == nil || o.seq < earliest.seq {
					earliest = o
				}
			}
		}
		per := (base + persons - 1) / persons
		share := int64(target.persons) * per
		if target == earliest {
			share -= per*persons - base
		}
		total += share
	}
	return total
}

func nSolo(o *nOrder, unitPrice int64) int64 {
	return int64(o.persons) * (o.dropoff - o.pickup) * unitPrice
}

func nPayable(raw, solo, cap int64) int64 {
	if raw > solo {
		raw = solo
	}
	if raw > cap {
		raw = cap
	}
	return raw
}

func (m *naiveModel) assign(o *nOrder, v *nVehicle) {
	o.vid = v.id
	if o.pickup <= v.pos {
		o.status = StatusOnboard
	} else {
		o.status = StatusMatched
	}
	o.cap = nPayable(m.rawFare(v, o), nSolo(o, m.cfg.UnitPrice), nSolo(o, m.cfg.UnitPrice))
}

func (m *naiveModel) submit(id string, pickup, dropoff int64, persons int, maxDelay, latest, now int64) (SubmitResult, error, string) {
	if id == "" || pickup < 0 || dropoff <= pickup || persons < 1 || maxDelay < 0 || latest < now {
		return SubmitResult{}, ErrInvalidParam, "参数非法"
	}
	if m.findOrder(id) != nil {
		return SubmitResult{}, ErrInvalidParam, "订单标识重复"
	}
	if now < m.now {
		return SubmitResult{}, ErrClockRollback, "时钟回退"
	}
	o := &nOrder{
		id: id, pickup: pickup, dropoff: dropoff, persons: persons,
		maxDelay: maxDelay, latest: latest, submit: now, status: StatusWaiting,
	}
	vid, ok, why := m.tryMatch(o, now)
	if !ok && latest <= now {
		return SubmitResult{}, ErrNoVehicle, why
	}
	o.seq = m.seq
	m.seq++
	m.orders = append(m.orders, o)
	if ok {
		m.assign(o, m.findVehicle(vid))
	}
	m.now = now
	m.expire()
	if ok {
		return SubmitResult{Status: o.status, VehicleID: vid}, nil, why
	}
	return SubmitResult{Status: StatusWaiting}, nil, why
}

func (m *naiveModel) cancel(id string, now int64) error {
	if id == "" {
		return ErrInvalidParam
	}
	if now < m.now {
		return ErrClockRollback
	}
	o := m.findOrder(id)
	if o == nil {
		return ErrOrderNotFound
	}
	switch o.status {
	case StatusCompleted:
		return ErrOrderCompleted
	case StatusOnboard:
		return ErrOrderOnboard
	case StatusCancelled, StatusExpired:
		return ErrInvalidParam
	}
	o.status = StatusCancelled
	o.settled = m.cfg.CancelFee
	m.now = now
	m.expire()
	return nil
}

func (m *naiveModel) update(vid string, pos, now int64) ([]Event, error) {
	if vid == "" || pos < 0 {
		return nil, ErrInvalidParam
	}
	if now < m.now {
		return nil, ErrClockRollback
	}
	v := m.findVehicle(vid)
	if v == nil {
		return nil, ErrVehicleNotFound
	}
	if pos < v.pos {
		return nil, ErrPositionRegression
	}
	v.pos = pos
	var events []Event
	// 上下车：按位置升序，同一位置先下车后上车。
	for {
		loc := int64(-1)
		for _, o := range m.activeOf(v.id) {
			if o.status == StatusOnboard && o.dropoff <= v.pos && (loc < 0 || o.dropoff < loc) {
				loc = o.dropoff
			}
			if o.status == StatusMatched && o.pickup <= v.pos && (loc < 0 || o.pickup < loc) {
				loc = o.pickup
			}
		}
		if loc < 0 {
			break
		}
		var completing []*nOrder
		for _, o := range m.activeOf(v.id) {
			if o.status == StatusOnboard && o.dropoff == loc {
				completing = append(completing, o)
			}
		}
		// 同一位置的所有完成订单以当时车上全部乘客结算，再统一归档。
		for _, o := range completing {
			o.settled = nPayable(m.rawFare(v, o), nSolo(o, m.cfg.UnitPrice), o.cap)
		}
		for _, o := range completing {
			o.status = StatusCompleted
			events = append(events, Event{Kind: EventCompleted, OrderID: o.id, VehicleID: v.id, Fare: o.settled})
		}
		for _, o := range m.activeOf(v.id) {
			if o.status == StatusMatched && o.pickup == loc {
				o.status = StatusOnboard
				events = append(events, Event{Kind: EventBoarded, OrderID: o.id, VehicleID: v.id})
			}
		}
	}
	// 等待订单按下单先后重试。
	for _, o := range m.waitingList() {
		if vid, ok, _ := m.tryMatch(o, now); ok {
			m.assign(o, m.findVehicle(vid))
			events = append(events, Event{Kind: EventMatched, OrderID: o.id, VehicleID: vid})
		}
	}
	m.now = now
	events = append(events, m.expire()...)
	return events, nil
}

// waitingList 全量扫描历史，按（下单时刻, 序号）返回等待订单。
func (m *naiveModel) waitingList() []*nOrder {
	var out []*nOrder
	for _, o := range m.orders {
		if o.status == StatusWaiting {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].submit != out[j].submit {
			return out[i].submit < out[j].submit
		}
		return out[i].seq < out[j].seq
	})
	return out
}

// expire 使最晚上车时刻不晚于当前时刻的等待订单失效。
func (m *naiveModel) expire() []Event {
	var events []Event
	for _, o := range m.orders {
		if o.status == StatusWaiting && o.latest <= m.now {
			o.status = StatusExpired
			events = append(events, Event{Kind: EventExpired, OrderID: o.id})
		}
	}
	return events
}

func (m *naiveModel) view(id string) (OrderView, error) {
	o := m.findOrder(id)
	if o == nil {
		return OrderView{}, ErrOrderNotFound
	}
	view := OrderView{
		ID: id, Status: o.status, VehicleID: o.vid,
		Cap: o.cap, SoloFare: nSolo(o, m.cfg.UnitPrice),
	}
	switch o.status {
	case StatusWaiting:
		view.EstimatedFare = view.SoloFare
	case StatusMatched, StatusOnboard:
		view.EstimatedFare = nPayable(m.rawFare(m.findVehicle(o.vid), o), view.SoloFare, o.cap)
	case StatusCompleted, StatusCancelled:
		view.EstimatedFare = o.settled
	}
	return view, nil
}

// ---- 随机操作序列对照 ----

type vehicleSpec struct {
	id    string
	seats int
	pos   int64
}

type testOp struct {
	kind             string // "submit" | "cancel" | "update"
	id               string // 订单或车辆标识
	a, b             int64  // submit: 上车点/下车点；update: 新位置
	persons          int
	maxDelay, latest int64
	now              int64
}

func genOps(rng *rand.Rand, cfg Config, steps int) ([]vehicleSpec, []testOp) {
	vehicles := []vehicleSpec{
		{id: "v-a", seats: 2, pos: 0},
		{id: "v-b", seats: 3, pos: 15},
		{id: "v-c", seats: 1, pos: 40},
	}
	var ops []testOp
	var now int64
	orderN := 0
	lastPos := map[string]int64{}
	for _, v := range vehicles {
		lastPos[v.id] = v.pos
	}
	for step := 0; step < steps; step++ {
		now += int64(rng.Intn(3))
		switch r := rng.Intn(100); {
		case r < 55:
			pickup := rng.Int63n(120)
			ops = append(ops, testOp{
				kind: "submit", id: fmt.Sprintf("o%d", orderN),
				a: pickup, b: pickup + 1 + rng.Int63n(60), persons: 1 + rng.Intn(2),
				maxDelay: int64(rng.Intn(6)) * cfg.StopDuration,
				latest:   now + 50 + rng.Int63n(300), now: now,
			})
			orderN++
		case r < 70:
			if orderN == 0 {
				continue
			}
			ops = append(ops, testOp{kind: "cancel", id: fmt.Sprintf("o%d", rng.Intn(orderN)), now: now})
		default:
			v := vehicles[rng.Intn(len(vehicles))]
			pos := lastPos[v.id] + rng.Int63n(40)
			if rng.Intn(10) == 0 {
				pos = rng.Int63n(200) // 偶发位置倒退，覆盖错误路径
			}
			if pos >= lastPos[v.id] {
				lastPos[v.id] = pos
			}
			ops = append(ops, testOp{kind: "update", id: v.id, a: pos, now: now})
		}
	}
	return vehicles, ops
}

func compareErr(t *testing.T, step int, serr, nerr error) {
	t.Helper()
	if (serr == nil) != (nerr == nil) {
		t.Fatalf("step %d: service err=%v, naive err=%v", step, serr, nerr)
	}
	if serr != nil && !errors.Is(serr, nerr) {
		t.Fatalf("step %d: service err=%v, naive err=%v", step, serr, nerr)
	}
}

func sortEvents(ev []Event) {
	sort.Slice(ev, func(i, j int) bool {
		if ev[i].Kind != ev[j].Kind {
			return ev[i].Kind < ev[j].Kind
		}
		if ev[i].OrderID != ev[j].OrderID {
			return ev[i].OrderID < ev[j].OrderID
		}
		if ev[i].VehicleID != ev[j].VehicleID {
			return ev[i].VehicleID < ev[j].VehicleID
		}
		return ev[i].Fare < ev[j].Fare
	})
}

// runSequence 把操作序列应用到新建的 Service；nv 非空时逐步与朴素模型对照
// 并打印每条操作的输入、输出与判定依据。返回逐步结果的序列化记录，
// 用于重放确定性比对。
func runSequence(t *testing.T, cfg Config, vehicles []vehicleSpec, ops []testOp, nv *naiveModel, log bool) []string {
	t.Helper()
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	for _, v := range vehicles {
		if err := svc.AddVehicle(v.id, v.seats, v.pos, 0); err != nil {
			t.Fatalf("AddVehicle: %v", err)
		}
		if nv != nil {
			nv.addVehicle(v.id, v.seats, v.pos)
		}
	}
	var record []string
	var orderIDs []string
	for step, op := range ops {
		switch op.kind {
		case "submit":
			sres, serr := svc.SubmitOrder(op.id, op.a, op.b, op.persons, op.maxDelay, op.latest, op.now)
			record = append(record, fmt.Sprintf("submit %s -> %+v err=%v", op.id, sres, serr))
			if nv != nil {
				nres, nerr, why := nv.submit(op.id, op.a, op.b, op.persons, op.maxDelay, op.latest, op.now)
				compareErr(t, step, serr, nerr)
				if serr == nil && sres != nres {
					t.Fatalf("step %d submit %s: service=%+v naive=%+v", step, op.id, sres, nres)
				}
				if log {
					t.Logf("step %d submit id=%s pickup=%d dropoff=%d persons=%d maxDelay=%d latest=%d now=%d"+
						" -> status=%v vehicle=%s err=%v 判定: %s",
						step, op.id, op.a, op.b, op.persons, op.maxDelay, op.latest, op.now,
						sres.Status, sres.VehicleID, serr, why)
				}
			}
			if serr == nil {
				orderIDs = append(orderIDs, op.id)
			}
		case "cancel":
			serr := svc.CancelOrder(op.id, op.now)
			record = append(record, fmt.Sprintf("cancel %s -> err=%v", op.id, serr))
			if nv != nil {
				nerr := nv.cancel(op.id, op.now)
				compareErr(t, step, serr, nerr)
				if log {
					sv, _ := svc.QueryOrder(op.id)
					t.Logf("step %d cancel id=%s now=%d -> err=%v status=%v", step, op.id, op.now, serr, sv.Status)
				}
			}
		case "update":
			sev, serr := svc.UpdateVehiclePosition(op.id, op.a, op.now)
			record = append(record, fmt.Sprintf("update %s pos=%d -> events=%v err=%v", op.id, op.a, sev, serr))
			if nv != nil {
				nev, nerr := nv.update(op.id, op.a, op.now)
				compareErr(t, step, serr, nerr)
				if serr == nil {
					sevCopy := append([]Event(nil), sev...)
					nevCopy := append([]Event(nil), nev...)
					sortEvents(sevCopy)
					sortEvents(nevCopy)
					if !reflect.DeepEqual(sevCopy, nevCopy) {
						t.Fatalf("step %d update %s: service events=%v naive events=%v", step, op.id, sev, nev)
					}
				}
				if log {
					t.Logf("step %d update vehicle=%s pos=%d now=%d -> events=%v err=%v",
						step, op.id, op.a, op.now, sev, serr)
				}
			}
		}
		// 逐步对照所有已接受订单的查询结果。
		for _, id := range orderIDs {
			sv, serr := svc.QueryOrder(id)
			record = append(record, fmt.Sprintf("view %s -> %+v err=%v", id, sv, serr))
			if nv != nil {
				nview, nerr := nv.view(id)
				if (serr == nil) != (nerr == nil) {
					t.Fatalf("step %d query %s: service err=%v naive err=%v", step, id, serr, nerr)
				}
				if serr == nil && sv != nview {
					t.Fatalf("step %d query %s:\n service=%+v\n naive =%+v", step, id, sv, nview)
				}
			}
		}
		for _, v := range vehicles {
			if err := svc.checkVehicleInvariant(v.id); err != nil {
				t.Fatalf("step %d invariant: %v", step, err)
			}
		}
	}
	return record
}

// 随机操作序列：Service 与独立朴素全量重算模型逐步对照，
// 并验证相同操作序列重放得到完全相同的匹配与金额。
func TestNaiveModelRandomOps(t *testing.T) {
	cfg := Config{StopDuration: 3, TimePerDistance: 2, UnitPrice: 5, CancelFee: 7, MaxActiveOrders: 3}
	seeds := []int64{20261006, 42, 7}
	for _, seed := range seeds {
		rng := rand.New(rand.NewSource(seed))
		vehicles, ops := genOps(rng, cfg, 250)
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			record := runSequence(t, cfg, vehicles, ops, newNaive(cfg), seed == seeds[0])
			replay := runSequence(t, cfg, vehicles, ops, nil, false)
			if !reflect.DeepEqual(record, replay) {
				t.Fatal("replay of the same op sequence produced different results")
			}
		})
	}
}
