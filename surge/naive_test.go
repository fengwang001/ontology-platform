package surge

import (
	"fmt"
	"math/rand"
	"testing"
)

// naiveModel 是与生产实现完全独立的朴素对照：
// 不维护任何增量计数，每次需要时全量扫描骑手与订单重算供需；
// 档位状态机与补贴规则按题目原文独立重写（仅复用纯函数目标档判定）。
type naiveModel struct {
	cfg     Config
	areas   map[string]bool
	riders  map[string]*nrider
	orders  map[string]*norder
	clock   int64
	tier    map[string]int
	confirm map[string]int
	lastEv  map[string]int64
	hasEv   map[string]bool
	events  map[string][]TierEvent
	subs    map[string][]SubsidyEntry
}

type nrider struct {
	online    bool
	area      string
	enteredAt int64
	held      int
}

type norder struct {
	area       string
	createdAt  int64
	state      orderState
	lockedTier int
	rider      string
	eligible   bool
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:     cfg,
		areas:   map[string]bool{},
		riders:  map[string]*nrider{},
		orders:  map[string]*norder{},
		tier:    map[string]int{},
		confirm: map[string]int{},
		lastEv:  map[string]int64{},
		hasEv:   map[string]bool{},
		events:  map[string][]TierEvent{},
		subs:    map[string][]SubsidyEntry{},
	}
}

// supplyDemand 全量重算：每次都扫描全部骑手与订单。
func (m *naiveModel) supplyDemand(area string) (available, pending int) {
	for _, r := range m.riders {
		if r.online && r.area == area && r.held < m.cfg.MaxHeldOrders {
			available++
		}
	}
	for _, o := range m.orders {
		if o.area == area && o.state == orderPending {
			pending++
		}
	}
	return
}

type opResult struct {
	kind string
	err  ErrorKind
	ok   bool
	note string
}

func (m *naiveModel) apply(kind string, at int64, x, y string) opResult {
	res := opResult{kind: kind}
	fail := func(k ErrorKind, note string) opResult {
		res.err = k
		res.note = note
		return res
	}
	if at < m.clock {
		return fail(KindClockRollback, "clock rollback")
	}
	switch kind {
	case "addArea":
		if m.areas[x] {
			return fail(KindInvalidParam, "dup area")
		}
		m.areas[x] = true
	case "addRider":
		if _, ok := m.riders[x]; ok {
			return fail(KindInvalidParam, "dup rider")
		}
		m.riders[x] = &nrider{}
	case "online":
		r, ok := m.riders[x]
		if !ok {
			return fail(KindRiderNotFound, "")
		}
		if !m.areas[y] {
			return fail(KindAreaNotFound, "")
		}
		if r.online {
			return fail(KindRiderAlreadyOnline, "")
		}
		r.online, r.area, r.enteredAt = true, y, at
	case "offline":
		r, ok := m.riders[x]
		if !ok {
			return fail(KindRiderNotFound, "")
		}
		if !r.online {
			return fail(KindRiderAlreadyOffline, "")
		}
		if r.held > 0 {
			return fail(KindRiderBusy, "")
		}
		r.online, r.area = false, ""
	case "move":
		r, ok := m.riders[x]
		if !ok {
			return fail(KindRiderNotFound, "")
		}
		if !r.online {
			return fail(KindRiderOffline, "")
		}
		if !m.areas[y] {
			return fail(KindAreaNotFound, "")
		}
		if r.area == y {
			return fail(KindNoNeedToMove, "")
		}
		r.area, r.enteredAt = y, at
	case "create":
		if !m.areas[y] {
			return fail(KindAreaNotFound, "")
		}
		if _, ok := m.orders[x]; ok {
			return fail(KindInvalidParam, "dup order")
		}
		m.orders[x] = &norder{area: y, createdAt: at, state: orderPending, lockedTier: m.tier[y]}
	case "cancel":
		o, ok := m.orders[x]
		if !ok {
			return fail(KindOrderNotFound, "")
		}
		switch o.state {
		case orderCancelled:
			return fail(KindOrderCancelled, "")
		case orderCompleted:
			return fail(KindOrderCompleted, "")
		case orderDispatched:
			return fail(KindOrderAlreadyDispatched, "")
		}
		o.state = orderCancelled
	case "dispatch":
		o, ook := m.orders[x]
		if !ook {
			return fail(KindOrderNotFound, "")
		}
		r, rok := m.riders[y]
		if !rok {
			return fail(KindRiderNotFound, "")
		}
		switch o.state {
		case orderCancelled:
			return fail(KindOrderCancelled, "")
		case orderCompleted:
			return fail(KindOrderCompleted, "")
		case orderDispatched:
			return fail(KindOrderAlreadyDispatched, "")
		}
		if !r.online {
			return fail(KindRiderOffline, "")
		}
		if r.area != o.area {
			return fail(KindRiderWrongArea, "")
		}
		if r.held >= m.cfg.MaxHeldOrders {
			return fail(KindRiderAtCapacity, "")
		}
		o.state = orderDispatched
		o.rider = y
		o.eligible = r.enteredAt <= o.createdAt
		r.held++
	case "complete":
		o, ok := m.orders[x]
		if !ok {
			return fail(KindOrderNotFound, "")
		}
		switch o.state {
		case orderCancelled:
			return fail(KindOrderCancelled, "")
		case orderCompleted:
			return fail(KindOrderCompleted, "")
		case orderPending:
			return fail(KindOrderAlreadyDispatched, "")
		}
		o.state = orderCompleted
		m.riders[o.rider].held--
		amount := int64(0)
		waived := !o.eligible
		if o.eligible {
			amount = m.cfg.Subsidies[o.lockedTier]
		}
		m.subs[o.rider] = append(m.subs[o.rider], SubsidyEntry{
			RiderID: o.rider, OrderID: x, At: at,
			Tier: o.lockedTier, Amount: amount, Waived: waived,
		})
	case "eval":
		if !m.areas[x] {
			return fail(KindAreaNotFound, "")
		}
		if m.hasEv[x] && at-m.lastEv[x] < m.cfg.MinEvalInterval {
			return fail(KindEvaluationTooFrequent, "")
		}
		avail, pend := m.supplyDemand(x)
		target, inf := targetTier(m.cfg.Thresholds, pend, avail)
		cur, conf := m.tier[x], m.confirm[x]
		newTier, newConf, changed := applyEvaluation(cur, conf, target, m.cfg.DowngradeConfirms)
		m.tier[x], m.confirm[x] = newTier, newConf
		m.lastEv[x], m.hasEv[x] = at, true
		res.note = fmt.Sprintf("avail=%d pend=%d target=%d %d->%d inf=%v changed=%v",
			avail, pend, target, cur, newTier, inf, changed)
		if changed {
			ratio := 0.0
			if avail > 0 {
				ratio = float64(pend) / float64(avail)
			}
			m.events[x] = append(m.events[x], TierEvent{
				Area: x, At: at, FromTier: cur, ToTier: newTier,
				Ratio: ratio, RatioInf: inf,
			})
		}
	}
	m.clock = at
	res.ok = true
	return res
}

type gen struct {
	rng    *rand.Rand
	areas  []string
	riders []string
	orders []string
	step   int
}

func (g *gen) next() (kind string, at int64, x, y string) {
	g.step++
	at = int64(g.step)
	if g.rng.Intn(8) == 0 {
		at = int64(g.step) - 1
	}
	if len(g.riders) < 2 || g.rng.Intn(6) == 0 {
		id := fmt.Sprintf("r%d", len(g.riders))
		g.riders = append(g.riders, id)
		return "addRider", at, id, ""
	}
	if g.rng.Intn(20) == 0 {
		id := fmt.Sprintf("area%d", len(g.areas))
		g.areas = append(g.areas, id)
		return "addArea", at, id, ""
	}
	r := g.riders[g.rng.Intn(len(g.riders))]
	a := g.areas[g.rng.Intn(len(g.areas))]
	switch g.rng.Intn(8) {
	case 0:
		return "online", at, r, a
	case 1:
		return "offline", at, r, ""
	case 2:
		return "move", at, r, a
	case 3:
		id := fmt.Sprintf("o%d", len(g.orders))
		g.orders = append(g.orders, id)
		return "create", at, id, a
	case 4:
		if len(g.orders) == 0 {
			return "eval", at, a, ""
		}
		return "cancel", at, g.orders[g.rng.Intn(len(g.orders))], ""
	case 5:
		if len(g.orders) == 0 {
			return "eval", at, a, ""
		}
		return "dispatch", at, g.orders[g.rng.Intn(len(g.orders))], r
	case 6:
		if len(g.orders) == 0 {
			return "eval", at, a, ""
		}
		return "complete", at, g.orders[g.rng.Intn(len(g.orders))], ""
	default:
		return "eval", at, a, ""
	}
}

func runProd(s *System, kind string, at int64, x, y string) opResult {
	res := opResult{kind: kind, ok: true}
	var e *Error
	switch kind {
	case "addArea":
		e = s.AddArea(x)
	case "addRider":
		e = s.AddRider(x)
	case "online":
		e = s.RiderOnline(x, y, at)
	case "offline":
		e = s.RiderOffline(x, at)
	case "move":
		e = s.RiderMove(x, y, at)
	case "create":
		_, e = s.CreateOrder(x, y, at)
	case "cancel":
		e = s.CancelOrder(x, at)
	case "dispatch":
		e = s.DispatchOrder(x, y, at)
	case "complete":
		_, _, e = s.CompleteOrder(x, at)
	case "eval":
		e = s.Evaluate(x, at)
	}
	if e != nil {
		res.ok, res.err = false, e.Kind
	}
	return res
}

func assertModelsAgree(t *testing.T, s *System, m *naiveModel) {
	t.Helper()
	for a := range m.areas {
		got, _ := s.CurrentTier(a)
		if got != m.tier[a] {
			t.Fatalf("tier mismatch %s: prod=%d naive=%d", a, got, m.tier[a])
		}
		sa := s.ledger.areas[a]
		if sa.downConfirms != m.confirm[a] {
			t.Fatalf("confirm mismatch %s: %d vs %d", a, sa.downConfirms, m.confirm[a])
		}
		ge, _ := s.TierEvents(a)
		ne := m.events[a]
		if len(ge) != len(ne) {
			t.Fatalf("events len %s: %d vs %d", a, len(ge), len(ne))
		}
		for i := range ge {
			if ge[i] != ne[i] {
				t.Fatalf("event mismatch: %+v vs %+v", ge[i], ne[i])
			}
		}
		av, pd := m.supplyDemand(a)
		if sa.available != av || sa.pending != pd {
			t.Fatalf("count mismatch %s: prod(av=%d pd=%d) naive(av=%d pd=%d)",
				a, sa.available, sa.pending, av, pd)
		}
	}
	for r := range m.riders {
		total, entries, _ := s.RiderSubsidy(r)
		var nt int64
		for _, en := range m.subs[r] {
			nt += en.Amount
		}
		if total != nt || len(entries) != len(m.subs[r]) {
			t.Fatalf("subsidy rider %s: prod(%d,%d) naive(%d,%d)",
				r, total, len(entries), nt, len(m.subs[r]))
		}
		for i := range entries {
			if entries[i] != m.subs[r][i] {
				t.Fatalf("subsidy entry mismatch: %+v vs %+v", entries[i], m.subs[r][i])
			}
		}
		if hr := s.ledger.riders[r].held; hr > s.cfg.MaxHeldOrders {
			t.Fatalf("rider %s held %d > cap %d", r, hr, s.cfg.MaxHeldOrders)
		}
	}
}

// TestNaiveDifferential 随机生成操作序列，逐步对比生产系统与朴素全量重算模型，
// 并打印每步输入、双方输出（接受/错误类别）与判定依据。
func TestNaiveDifferential(t *testing.T) {
	if testing.Verbose() {
		t.Logf("step | input | prod -> naive | basis")
	}
	for seed := int64(1); seed <= 40; seed++ {
		cfg := Config{
			Thresholds:        []float64{1, 2, 4},
			DowngradeConfirms: 1 + int(seed%3),
			MaxHeldOrders:     1 + int(seed%3),
			Subsidies:         []int64{0, 10, 20, 30},
			MinEvalInterval:   3 + seed%5,
		}
		s, e := New(cfg)
		must(t, e)
		m := newNaive(cfg)
		g := &gen{rng: rand.New(rand.NewSource(seed)), areas: []string{"A0"}, step: 0}
		s.AddArea("A0")
		m.areas["A0"] = true
		for i := 0; i < 400; i++ {
			kind, at, x, y := g.next()
			pr := runProd(s, kind, at, x, y)
			nr := m.apply(kind, at, x, y)
			if testing.Verbose() {
				t.Logf("seed=%d #%d %-9s at=%d x=%s y=%s | prod ok=%v err=%d | naive ok=%v err=%d | %s",
					seed, i, kind, at, x, y, pr.ok, pr.err, nr.ok, nr.err, nr.note)
			}
			if pr.ok != nr.ok || pr.err != nr.err {
				t.Fatalf("seed=%d step=%d %s(%s,%s) at=%d divergence: prod ok=%v err=%d naive ok=%v err=%d",
					seed, i, kind, x, y, at, pr.ok, pr.err, nr.ok, nr.err)
			}
			assertModelsAgree(t, s, m)
		}
	}
}
