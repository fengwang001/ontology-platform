package dispatch

// 本文件是一个独立实现的朴素对照模型：穷举所有插入位置、每次从头重算推定，
// 与 System 的随机操作序列逐步对拍，并打印每步输入、输出与判定依据。

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type mStop struct {
	order  string
	pickup bool
	dwell  int64
	cap    int64
}

type mOrder struct {
	o      Order
	status orderStatus
	rider  string
}

type mRider struct {
	r      Rider
	online bool
	stops  []mStop
	held   int
}

type model struct {
	src    TravelTimeSource
	detour int64
	clock  int64
	riders map[string]*mRider
	orders map[string]*mOrder
}

func newModel(detour int64, src TravelTimeSource) *model {
	return &model{
		detour: detour,
		src:    src,
		riders: make(map[string]*mRider),
		orders: make(map[string]*mOrder),
	}
}

// project 独立重算整条序列的推定到达/离开时刻。
func (m *model) project(r *mRider) (eta, leave []int64) {
	eta = make([]int64, len(r.stops))
	leave = make([]int64, len(r.stops))
	cur := r.r.Pos
	t := r.r.DepartAt
	for i, st := range r.stops {
		o := m.orders[st.order].o
		pt := o.Drop
		if st.pickup {
			pt = o.Pickup
		}
		e := t + m.src.TravelTime(cur, pt)
		if e > st.cap {
			e = st.cap
		}
		eta[i] = e
		lv := e
		if st.pickup && lv < o.ReadyAt {
			lv = o.ReadyAt
		}
		leave[i] = lv + st.dwell
		cur = pt
		t = leave[i]
	}
	return eta, leave
}

func (m *model) addRider(r Rider, t int64) string {
	if r.ID == "" || r.Region == "" || r.Pos == "" || r.Capacity < 1 || r.DepartAt < 0 {
		return "invalid"
	}
	if t < m.clock {
		return "clock"
	}
	if _, ok := m.riders[r.ID]; ok {
		return "rider-exists"
	}
	m.riders[r.ID] = &mRider{r: r, online: true}
	m.clock = t
	return "ok"
}

func (m *model) setOnline(riderID string, online bool, t int64) string {
	if riderID == "" {
		return "invalid"
	}
	if t < m.clock {
		return "clock"
	}
	r, ok := m.riders[riderID]
	if !ok {
		return "no-rider"
	}
	r.online = online
	m.clock = t
	return "ok"
}

func (m *model) createOrder(o Order, t int64) string {
	if o.ID == "" || o.Region == "" || o.Pickup == "" || o.Drop == "" ||
		o.ReadyAt < 0 || o.PromiseAt < o.ReadyAt || o.PickupDwell < 0 || o.DropDwell < 0 {
		return "invalid"
	}
	if t < m.clock {
		return "clock"
	}
	if _, ok := m.orders[o.ID]; ok {
		return "order-exists"
	}
	m.orders[o.ID] = &mOrder{o: o, status: statusCreated}
	m.clock = t
	return "ok"
}

// scan 穷举 (p,q)，每个候选都从头完整重算，不做任何复用。
func (m *model) scan(r *mRider, mo *mOrder) (best insertion, feasible, promiseOK bool) {
	n := len(r.stops)
	baseEta, baseLeave := m.project(r)
	var baseTotal int64
	if n > 0 {
		baseTotal = baseLeave[n-1] - r.r.DepartAt
	}
	baseDrop := make(map[string]int64)
	for i, st := range r.stops {
		if !st.pickup {
			baseDrop[st.order] = baseEta[i]
		}
	}
	first := true
	for p := 0; p <= n; p++ {
		for q := p + 1; q <= n+1; q++ {
			cand := make([]mStop, 0, n+2)
			cand = append(cand, r.stops[:p]...)
			cand = append(cand, mStop{order: mo.o.ID, pickup: true, dwell: mo.o.PickupDwell, cap: noEtaCap})
			cand = append(cand, r.stops[p:q-1]...)
			cand = append(cand, mStop{order: mo.o.ID, dwell: mo.o.DropDwell, cap: noEtaCap})
			cand = append(cand, r.stops[q-1:]...)
			saved := r.stops
			r.stops = cand
			eta, leave := m.project(r)
			r.stops = saved
			dropEta := eta[q]
			if dropEta > mo.o.PromiseAt {
				continue
			}
			promiseOK = true
			ok := true
			var sumDelay int64
			for i, st := range cand {
				if st.pickup || st.order == mo.o.ID {
					continue
				}
				d := eta[i] - baseDrop[st.order]
				if d > 0 {
					sumDelay += d
				}
				if eta[i] > m.orders[st.order].o.PromiseAt || d > m.detour {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			var total int64
			if len(leave) > 0 {
				total = leave[len(leave)-1] - r.r.DepartAt
			}
			cur := insertion{p: p, q: q, sumDelay: sumDelay, dropEta: dropEta, increment: total - baseTotal}
			better := first ||
				cur.sumDelay < best.sumDelay ||
				(cur.sumDelay == best.sumDelay && cur.dropEta < best.dropEta) ||
				(cur.sumDelay == best.sumDelay && cur.dropEta == best.dropEta && cur.p < best.p) ||
				(cur.sumDelay == best.sumDelay && cur.dropEta == best.dropEta && cur.p == best.p && cur.q < best.q)
			if better {
				best, first = cur, false
			}
		}
	}
	feasible = !first
	return best, feasible, promiseOK
}

func (m *model) dispatch(orderID string, t int64) (string, Assignment) {
	if orderID == "" {
		return "invalid", Assignment{}
	}
	if t < m.clock {
		return "clock", Assignment{}
	}
	mo, ok := m.orders[orderID]
	if !ok {
		return "no-order", Assignment{}
	}
	switch mo.status {
	case statusAssigned, statusPicked:
		return "assigned", Assignment{}
	case statusDelivered, statusCancelled:
		return "not-assignable", Assignment{}
	}
	var ids []string
	for id, r := range m.riders {
		if r.r.Region == mo.o.Region && r.online {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	allFull := true
	anyPromise := false
	found := false
	var bestR *mRider
	var bestIns insertion
	for _, id := range ids {
		r := m.riders[id]
		if r.held >= r.r.Capacity {
			continue
		}
		allFull = false
		ins, feas, prom := m.scan(r, mo)
		if prom {
			anyPromise = true
		}
		if !feas {
			continue
		}
		if !found ||
			ins.increment < bestIns.increment ||
			(ins.increment == bestIns.increment && r.held < bestR.held) ||
			(ins.increment == bestIns.increment && r.held == bestR.held && r.r.ID < bestR.r.ID) {
			bestR, bestIns, found = r, ins, true
		}
	}
	if !found {
		switch {
		case allFull:
			return "capacity", Assignment{}
		case !anyPromise:
			return "promise", Assignment{}
		default:
			return "detour", Assignment{}
		}
	}
	pk := mStop{order: mo.o.ID, pickup: true, dwell: mo.o.PickupDwell, cap: noEtaCap}
	dl := mStop{order: mo.o.ID, dwell: mo.o.DropDwell, cap: noEtaCap}
	n := len(bestR.stops)
	cand := make([]mStop, 0, n+2)
	cand = append(cand, bestR.stops[:bestIns.p]...)
	cand = append(cand, pk)
	cand = append(cand, bestR.stops[bestIns.p:bestIns.q-1]...)
	cand = append(cand, dl)
	cand = append(cand, bestR.stops[bestIns.q-1:]...)
	bestR.stops = cand
	bestR.held++
	mo.status = statusAssigned
	mo.rider = bestR.r.ID
	m.clock = t
	return "ok", Assignment{
		RiderID:    bestR.r.ID,
		PickupPos:  bestIns.p,
		DeliverPos: bestIns.q,
		DropEta:    bestIns.dropEta,
		Increment:  bestIns.increment,
	}
}

func (m *model) complete(riderID, orderID string, kind StopKind, t int64) string {
	if riderID == "" || orderID == "" || (kind != StopPickup && kind != StopDeliver) {
		return "invalid"
	}
	if t < m.clock {
		return "clock"
	}
	r, ok := m.riders[riderID]
	if !ok {
		return "no-rider"
	}
	if !r.online {
		return "offline"
	}
	wantPickup := kind == StopPickup
	if len(r.stops) == 0 || r.stops[0].order != orderID || r.stops[0].pickup != wantPickup {
		return "out-of-order"
	}
	st := r.stops[0]
	mo := m.orders[st.order]
	leave := t
	pt := mo.o.Drop
	if st.pickup {
		pt = mo.o.Pickup
		if leave < mo.o.ReadyAt {
			leave = mo.o.ReadyAt
		}
	}
	r.r.Pos = pt
	r.r.DepartAt = leave
	r.stops = r.stops[1:]
	for i := range r.stops {
		r.stops[i].cap = noEtaCap
	}
	if st.pickup {
		mo.status = statusPicked
	} else {
		mo.status = statusDelivered
		r.held--
	}
	m.clock = t
	return "ok"
}

func (m *model) cancel(orderID string, t int64) string {
	if orderID == "" {
		return "invalid"
	}
	if t < m.clock {
		return "clock"
	}
	mo, ok := m.orders[orderID]
	if !ok {
		return "no-order"
	}
	switch mo.status {
	case statusPicked, statusDelivered:
		return "picked"
	case statusCancelled:
		return "cancelled"
	case statusCreated:
		mo.status = statusCancelled
		m.clock = t
		return "ok"
	}
	r := m.riders[mo.rider]
	eta, _ := m.project(r)
	var kept []mStop
	for i, st := range r.stops {
		if st.order == orderID {
			continue
		}
		if eta[i] < st.cap {
			st.cap = eta[i]
		}
		kept = append(kept, st)
	}
	r.stops = kept
	r.held--
	mo.status = statusCancelled
	mo.rider = ""
	m.clock = t
	return "ok"
}

// routeDesc 输出模型某骑手的停靠序列描述，用于与 System.Route 对比。
func (m *model) routeDesc(riderID string) string {
	r, ok := m.riders[riderID]
	if !ok {
		return "missing"
	}
	eta, leave := m.project(r)
	var b strings.Builder
	fmt.Fprintf(&b, "held=%d;", r.held)
	for i, st := range r.stops {
		kind := "D"
		if st.pickup {
			kind = "P"
		}
		fmt.Fprintf(&b, "%s%s@%d/%d;", st.order, kind, eta[i], leave[i])
	}
	return b.String()
}

func sysRouteDesc(s *System, riderID string) string {
	sv, err := s.Route(riderID)
	if err != nil {
		return "missing"
	}
	held, _ := s.Held(riderID)
	var b strings.Builder
	fmt.Fprintf(&b, "held=%d;", held)
	for _, v := range sv {
		kind := "D"
		if v.Kind == StopPickup {
			kind = "P"
		}
		fmt.Fprintf(&b, "%s%s@%d/%d;", v.OrderID, kind, v.Eta, v.Leave)
	}
	return b.String()
}

// codeOf 把 System 的错误映射为与模型一致的结果码。
func codeOf(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParam):
		return "invalid"
	case errors.Is(err, ErrClockRegression):
		return "clock"
	case errors.Is(err, ErrRiderNotFound):
		return "no-rider"
	case errors.Is(err, ErrRiderExists):
		return "rider-exists"
	case errors.Is(err, ErrRiderOffline):
		return "offline"
	case errors.Is(err, ErrOrderNotFound):
		return "no-order"
	case errors.Is(err, ErrOrderExists):
		return "order-exists"
	case errors.Is(err, ErrOrderAlreadyAssigned):
		return "assigned"
	case errors.Is(err, ErrOrderNotAssignable):
		return "not-assignable"
	case errors.Is(err, ErrOrderAlreadyPickedUp):
		return "picked"
	case errors.Is(err, ErrOrderAlreadyCancelled):
		return "cancelled"
	case errors.Is(err, ErrStopOutOfOrder):
		return "out-of-order"
	case errors.Is(err, ErrNoRiderCapacity):
		return "capacity"
	case errors.Is(err, ErrNoPromisePosition):
		return "promise"
	case errors.Is(err, ErrDetourLimit):
		return "detour"
	}
	return "unknown"
}

// op 是一条可重放的操作。
type op struct {
	desc    string
	rider   Rider
	order   Order
	riderID string
	orderID string
	kind    StopKind
	online  bool
	t       int64
	action  string // addrider/online/create/dispatch/complete/cancel
}

// genOps 生成确定性的随机操作序列（含少量时钟回退与非法操作）。
func genOps(seed int64, steps int) ([]op, []string, TravelTimeSource) {
	rng := rand.New(rand.NewSource(seed))
	points := []Point{"P0", "P1", "P2", "P3", "P4", "P5"}
	regions := []string{"g0", "g1"}
	// 随机耗时矩阵：非对称、不满足三角不等式。
	m := make(map[[2]Point]int64)
	for _, a := range points {
		for _, b := range points {
			m[[2]Point{a, b}] = int64(1 + rng.Intn(60))
		}
	}
	src := tableSrc{m: m, def: 7}
	var ops []op
	var riderIDs []string
	cur := int64(0)
	nextT := func() int64 {
		if rng.Intn(100) < 8 && cur >= 5 {
			return cur - 5 // 时钟回退尝试
		}
		cur += int64(rng.Intn(4))
		return cur
	}
	orderN, riderN := 0, 0
	addRider := func() {
		r := Rider{
			ID:       fmt.Sprintf("r%d", riderN),
			Region:   regions[rng.Intn(len(regions))],
			Pos:      points[rng.Intn(len(points))],
			DepartAt: cur,
			Capacity: 1 + rng.Intn(2),
		}
		riderN++
		riderIDs = append(riderIDs, r.ID)
		ops = append(ops, op{action: "addrider", rider: r, t: nextT(),
			desc: fmt.Sprintf("AddRider(%+v)", r)})
	}
	for i := 0; i < 3; i++ {
		addRider()
	}
	for i := 0; i < steps; i++ {
		switch x := rng.Intn(100); {
		case x < 28: // 创建订单（偶尔重复 ID）
			id := fmt.Sprintf("o%d", orderN)
			if orderN > 0 && rng.Intn(100) < 10 {
				id = fmt.Sprintf("o%d", rng.Intn(orderN))
			} else {
				orderN++
			}
			ready := cur + int64(rng.Intn(20))
			o := Order{
				ID:          id,
				Region:      regions[rng.Intn(len(regions))],
				Pickup:      points[rng.Intn(len(points))],
				Drop:        points[rng.Intn(len(points))],
				ReadyAt:     ready,
				PromiseAt:   ready + int64(rng.Intn(150)),
				PickupDwell: int64(rng.Intn(4)),
				DropDwell:   int64(rng.Intn(3)),
			}
			ops = append(ops, op{action: "create", order: o, t: nextT(),
				desc: fmt.Sprintf("CreateOrder(%+v)", o)})
		case x < 55: // 派单（含不存在/已派订单）
			id := fmt.Sprintf("o%d", rng.Intn(orderN+2))
			ops = append(ops, op{action: "dispatch", orderID: id, t: nextT(),
				desc: fmt.Sprintf("DispatchOrder(%s)", id)})
		case x < 75: // 完成停靠（70% 报首停靠，30% 随机可能乱序）
			rid := riderIDs[rng.Intn(len(riderIDs))]
			ops = append(ops, op{action: "complete", riderID: rid,
				online: rng.Intn(100) < 70, t: nextT(),
				desc: fmt.Sprintf("CompleteStop(%s)", rid)})
		case x < 88: // 取消订单
			id := fmt.Sprintf("o%d", rng.Intn(orderN+1))
			ops = append(ops, op{action: "cancel", orderID: id, t: nextT(),
				desc: fmt.Sprintf("CancelOrder(%s)", id)})
		case x < 96: // 上下线
			rid := riderIDs[rng.Intn(len(riderIDs))]
			on := rng.Intn(100) < 70
			ops = append(ops, op{action: "online", riderID: rid, online: on, t: nextT(),
				desc: fmt.Sprintf("SetOnline(%s,%v)", rid, on)})
		default: // 新骑手
			addRider()
		}
	}
	return ops, riderIDs, src
}

// applySys 把一条操作应用到 System，返回结果码与判定依据。
func applySys(s *System, o op) (string, string) {
	switch o.action {
	case "addrider":
		return codeOf(s.AddRider(o.rider, o.t)), ""
	case "online":
		return codeOf(s.SetOnline(o.riderID, o.online, o.t)), ""
	case "create":
		return codeOf(s.CreateOrder(o.order, o.t)), ""
	case "dispatch":
		asg, err := s.DispatchOrder(o.orderID, o.t)
		if err != nil {
			return codeOf(err), ""
		}
		return "ok", fmt.Sprintf("rider=%s p=%d q=%d eta=%d inc=%d",
			asg.RiderID, asg.PickupPos, asg.DeliverPos, asg.DropEta, asg.Increment)
	case "complete":
		return codeOf(s.CompleteStop(o.riderID, o.orderID, o.kind, o.t)), ""
	case "cancel":
		return codeOf(s.CancelOrder(o.orderID, o.t)), ""
	}
	panic("bad action")
}

// applyModel 把同一条操作应用到对照模型。
func applyModel(m *model, o op) (string, string) {
	switch o.action {
	case "addrider":
		return m.addRider(o.rider, o.t), ""
	case "online":
		return m.setOnline(o.riderID, o.online, o.t), ""
	case "create":
		return m.createOrder(o.order, o.t), ""
	case "dispatch":
		code, asg := m.dispatch(o.orderID, o.t)
		if code != "ok" {
			return code, ""
		}
		return "ok", fmt.Sprintf("rider=%s p=%d q=%d eta=%d inc=%d",
			asg.RiderID, asg.PickupPos, asg.DeliverPos, asg.DropEta, asg.Increment)
	case "complete":
		return m.complete(o.riderID, o.orderID, o.kind, o.t), ""
	case "cancel":
		return m.cancel(o.orderID, o.t), ""
	}
	panic("bad action")
}

// resolveComplete 把 complete 操作具体化：online=true 报首停靠，否则随机一个
// 可能乱序的停靠。System 与模型用同一具体化，保证输入一致。
func resolveComplete(s *System, o op, rng *rand.Rand) op {
	if o.action != "complete" {
		return o
	}
	sv, err := s.Route(o.riderID)
	if err != nil || len(sv) == 0 {
		o.orderID, o.kind = "ghost", StopPickup
		return o
	}
	idx := 0
	if !o.online { // 复用 online 字段作为“是否首停靠”标记
		idx = rng.Intn(len(sv))
	}
	o.orderID, o.kind = sv[idx].OrderID, sv[idx].Kind
	o.desc = fmt.Sprintf("CompleteStop(%s,%s,%v)", o.riderID, o.orderID, o.kind)
	return o
}

// TestRandomModelComparison 用固定种子的随机操作序列对 System 与朴素模型
// 逐步对拍：每步比较结果码、派单明细与全部骑手的停靠推定。
func TestRandomModelComparison(t *testing.T) {
	for seed := int64(1); seed <= 6; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			ops, riderIDs, src := genOps(seed, 200)
			s := New(Config{MaxDetour: 40}, src)
			m := newModel(40, src)
			rng := rand.New(rand.NewSource(seed + 1000))
			for i, o := range ops {
				o = resolveComplete(s, o, rng)
				codeS, detailS := applySys(s, o)
				codeM, detailM := applyModel(m, o)
				t.Logf("step %3d t=%-4d in=%-60s out=%s %s", i, o.t, o.desc, codeS, detailS)
				if codeS != codeM || detailS != detailM {
					t.Fatalf("step %d %s: system=(%s %s) model=(%s %s)",
						i, o.desc, codeS, detailS, codeM, detailM)
				}
				for _, rid := range riderIDs {
					ds, dm := sysRouteDesc(s, rid), m.routeDesc(rid)
					if ds != dm {
						t.Fatalf("step %d %s: route %s diverged: system=%s model=%s",
							i, o.desc, rid, ds, dm)
					}
				}
			}
		})
	}
}

// TestReplayDeterminism 同一操作序列重放两次，结果必须完全一致。
func TestReplayDeterminism(t *testing.T) {
	run := func() []string {
		ops, _, src := genOps(99, 200)
		s := New(Config{MaxDetour: 40}, src)
		rng := rand.New(rand.NewSource(1099))
		var log []string
		for _, o := range ops {
			o = resolveComplete(s, o, rng)
			code, detail := applySys(s, o)
			log = append(log, code+" "+detail)
		}
		return log
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("log length %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("step %d diverged: %q != %q", i, a[i], b[i])
		}
	}
}
