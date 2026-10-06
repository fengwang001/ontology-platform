package maintenance

// naiveModel 是独立编写的参考实现：所有选择都对承包商/工单做线性扫描，
// 不使用堆或索引。它与 Service 共用错误哨兵和等级常量，但不复用任何
// Service 的方法，用于随机操作序列对照。

type nContractor struct {
	id               int
	trades           map[string]bool
	buildings        map[string]bool
	capacity         int
	acceptsEmergency bool
	active           bool
	holds            map[int]bool
	completedBefore  bool
	lastCompletion   int
}

func (c *nContractor) count(orders map[int]*nOrder) int {
	n := 0
	for id := range c.holds {
		if orders[id].status != StatusCancelled {
			n++
		}
	}
	return n
}

type nOrder struct {
	id, submittedAt                     int
	tenant, trade, building             string
	level                               Level
	status                              Status
	assignedTo, dispatchedAt            int
	responseDue, completeDue, confirmed int
	rejects                             int
	rejectedBy                          map[int]bool
	overdueEmitted                      bool
}

type nEvent struct {
	at     int
	typ    EventType
	order  int
	who    int
	newLev Level
}

type naiveModel struct {
	cfg          Config
	now          int
	hasNow       bool
	contractors  map[int]*nContractor
	orders       map[int]*nOrder
	nextC, nextO int
	events       []nEvent
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:         cfg,
		contractors: map[int]*nContractor{},
		orders:      map[int]*nOrder{},
	}
}

func (m *naiveModel) emit(at int, typ EventType, order, who int, lv Level) {
	m.events = append(m.events, nEvent{at: at, typ: typ, order: order, who: who, newLev: lv})
}

// advance 实现与 Service 相同的时钟规则：非法参数由调用方先行校验，
// 通过参数校验后回退即报错且不改变任何状态。
func (m *naiveModel) advance(at int) error {
	if m.hasNow && at < m.now {
		return ErrClockBackward
	}
	m.hasNow = true
	m.now = at
	return nil
}

func (m *naiveModel) returnOrder(o *nOrder, at int, by int, rejected bool) {
	c := m.contractors[o.assignedTo]
	delete(c.holds, o.id)
	o.assignedTo = 0
	o.dispatchedAt = 0
	o.responseDue = 0
	o.completeDue = 0
	o.status = StatusQueued
	if rejected {
		o.rejects++
		if by != 0 {
			o.rejectedBy[by] = true
		}
		m.emit(at, EventRejected, o.id, by, 0)
		if o.level < LevelEmergency && o.rejects%m.cfg.RejectUpgrade == 0 {
			o.level++
			m.emit(at, EventUpgraded, o.id, 0, o.level)
		}
	} else {
		m.emit(at, EventReturned, o.id, by, 0)
	}
}

// settle 按工单序号依次结算响应逾期与完成逾期。
func (m *naiveModel) settle(at int) {
	for id := 1; id <= m.nextO; id++ {
		o, ok := m.orders[id]
		if !ok {
			continue
		}
		switch o.status {
		case StatusDispatched:
			if at > o.responseDue {
				m.returnOrder(o, at, o.assignedTo, true)
			}
		case StatusConfirmed:
			if at > o.completeDue && !o.overdueEmitted {
				o.status = StatusOverdue
				o.overdueEmitted = true
				m.emit(at, EventOverdue, o.id, o.assignedTo, 0)
			}
		}
	}
}

func (m *naiveModel) better(c, d *nContractor) bool {
	cc, dd := c.count(m.orders), d.count(m.orders)
	if cc != dd {
		return cc < dd
	}
	if c.completedBefore != d.completedBefore {
		return !c.completedBefore
	}
	if c.completedBefore && c.lastCompletion != d.lastCompletion {
		return c.lastCompletion < d.lastCompletion
	}
	return c.id < d.id
}

func (m *naiveModel) eligible(c *nContractor, o *nOrder) bool {
	if !c.active || c.count(m.orders) >= c.capacity {
		return false
	}
	if !c.trades[o.trade] || !c.buildings[o.building] {
		return false
	}
	if o.level == LevelEmergency && !c.acceptsEmergency {
		return false
	}
	return !o.rejectedBy[c.id]
}

func (m *naiveModel) victim(c *nContractor) *nOrder {
	var v *nOrder
	for oid := range c.holds {
		o := m.orders[oid]
		if o.status != StatusDispatched || o.level == LevelEmergency {
			continue
		}
		if v == nil || o.dispatchedAt > v.dispatchedAt ||
			(o.dispatchedAt == v.dispatchedAt && o.id > v.id) {
			v = o
		}
	}
	return v
}

// queueAll 线性重建队列次序：等级高->提交早->序号小。
func (m *naiveModel) queuedOrders() []*nOrder {
	var qs []*nOrder
	for id := 1; id <= m.nextO; id++ {
		if o, ok := m.orders[id]; ok && o.status == StatusQueued {
			qs = append(qs, o)
		}
	}
	for i := 0; i < len(qs); i++ {
		for j := i + 1; j < len(qs); j++ {
			a, b := qs[i], qs[j]
			// less 表示 b 应排在 a 之前，则交换使前面元素优先。
			less := false
			if a.level != b.level {
				less = b.level > a.level
			} else if a.submittedAt != b.submittedAt {
				less = b.submittedAt < a.submittedAt
			} else {
				less = b.id < a.id
			}
			if less {
				qs[i], qs[j] = qs[j], qs[i]
			}
		}
	}
	return qs
}

func (m *naiveModel) dispatch(at int) (int, int, error) {
	if err := m.advance(at); err != nil {
		return 0, 0, err
	}
	m.settle(at)
	var deferred []*nOrder
	for _, o := range m.queuedOrders() {
		var best *nContractor
		for id := 1; id <= m.nextC; id++ {
			c, ok := m.contractors[id]
			if !ok || !m.eligible(c, o) {
				continue
			}
			if best == nil || m.better(c, best) {
				best = c
			}
		}
		if best == nil && o.level == LevelEmergency {
			var pb *nContractor
			for id := 1; id <= m.nextC; id++ {
				c, ok := m.contractors[id]
				if !ok || !c.active || c.count(m.orders) < c.capacity || !c.acceptsEmergency {
					continue
				}
				if !c.trades[o.trade] || !c.buildings[o.building] || o.rejectedBy[c.id] {
					continue
				}
				if m.victim(c) == nil {
					continue
				}
				if pb == nil || m.better(c, pb) {
					pb = c
				}
			}
			if pb != nil {
				v := m.victim(pb)
				delete(pb.holds, v.id)
				v.assignedTo = 0
				v.dispatchedAt = 0
				v.responseDue = 0
				v.completeDue = 0
				v.status = StatusQueued
				m.emit(at, EventPreempted, v.id, pb.id, 0)
				best = pb
			}
		}
		if best == nil {
			deferred = append(deferred, o)
			continue
		}
		o.status = StatusDispatched
		o.assignedTo = best.id
		o.dispatchedAt = at
		o.responseDue = at + m.cfg.ResponseLimit[o.level-1]
		o.completeDue = at + m.cfg.CompleteLimit[o.level-1]
		best.holds[o.id] = true
		m.emit(at, EventDispatched, o.id, best.id, 0)
		_ = deferred
		return o.id, best.id, nil
	}
	return 0, 0, ErrNoCandidate
}

func (m *naiveModel) register(at int, trades, buildings []string, cap int, emerg bool) (int, error) {
	if !nNonEmpty(trades) || !nNonEmpty(buildings) || cap <= 0 {
		return 0, ErrInvalidArgument
	}
	if err := m.advance(at); err != nil {
		return 0, err
	}
	m.nextC++
	c := &nContractor{
		id: m.nextC, trades: map[string]bool{}, buildings: map[string]bool{},
		capacity: cap, acceptsEmergency: emerg, active: true, holds: map[int]bool{},
	}
	for _, t := range trades {
		c.trades[t] = true
	}
	for _, b := range buildings {
		c.buildings[b] = true
	}
	m.contractors[c.id] = c
	return c.id, nil
}

func nNonEmpty(xs []string) bool {
	if len(xs) == 0 {
		return false
	}
	for _, x := range xs {
		if x == "" {
			return false
		}
	}
	return true
}

func (m *naiveModel) submit(at int, tenant, trade, building string, lv Level) (int, error) {
	if tenant == "" || trade == "" || building == "" || !lv.valid() {
		return 0, ErrInvalidArgument
	}
	if err := m.advance(at); err != nil {
		return 0, err
	}
	m.settle(at)
	m.nextO++
	o := &nOrder{
		id: m.nextO, submittedAt: at, tenant: tenant, trade: trade,
		building: building, level: lv, status: StatusQueued,
		rejectedBy: map[int]bool{},
	}
	m.orders[o.id] = o
	return o.id, nil
}

func (m *naiveModel) confirm(at, oid, cid int) error {
	if err := m.advance(at); err != nil {
		return err
	}
	m.settle(at)
	o, ook := m.orders[oid]
	_, cok := m.contractors[cid]
	if !ook || !cok {
		return ErrNotFound
	}
	if o.status != StatusDispatched {
		return ErrInvalidState
	}
	if o.assignedTo != cid {
		return ErrForbidden
	}
	o.status = StatusConfirmed
	o.confirmed = at
	return nil
}

func (m *naiveModel) reject(at, oid, cid int) error {
	if err := m.advance(at); err != nil {
		return err
	}
	m.settle(at)
	o, ook := m.orders[oid]
	_, cok := m.contractors[cid]
	if !ook || !cok {
		return ErrNotFound
	}
	if o.status != StatusDispatched {
		return ErrInvalidState
	}
	if o.assignedTo != cid {
		return ErrForbidden
	}
	m.returnOrder(o, at, cid, true)
	return nil
}

func (m *naiveModel) complete(at, cid, oid int) error {
	if err := m.advance(at); err != nil {
		return err
	}
	m.settle(at)
	o, ook := m.orders[oid]
	_, cok := m.contractors[cid]
	if !ook || !cok {
		return ErrNotFound
	}
	if o.status != StatusConfirmed && o.status != StatusOverdue {
		return ErrInvalidState
	}
	if o.assignedTo != cid {
		return ErrForbidden
	}
	c := m.contractors[cid]
	delete(c.holds, o.id)
	o.status = StatusCompleted
	o.assignedTo = 0
	c.completedBefore = true
	c.lastCompletion = at
	return nil
}

func (m *naiveModel) cancel(at int, tenant string, oid int) error {
	if tenant == "" {
		return ErrInvalidArgument
	}
	if err := m.advance(at); err != nil {
		return err
	}
	m.settle(at)
	o, ok := m.orders[oid]
	if !ok {
		return ErrNotFound
	}
	if o.status != StatusQueued && o.status != StatusDispatched {
		return ErrInvalidState
	}
	if o.tenant != tenant {
		return ErrForbidden
	}
	if o.status == StatusDispatched {
		delete(m.contractors[o.assignedTo].holds, o.id)
		o.assignedTo = 0
		o.dispatchedAt = 0
		o.responseDue = 0
		o.completeDue = 0
	}
	o.status = StatusCancelled
	return nil
}

func (m *naiveModel) deactivate(at, cid int) error {
	if err := m.advance(at); err != nil {
		return err
	}
	m.settle(at)
	c, ok := m.contractors[cid]
	if !ok {
		return ErrNotFound
	}
	if !c.active {
		return ErrInvalidState
	}
	c.active = false
	for oid := range c.holds {
		o := m.orders[oid]
		if o.status == StatusDispatched {
			m.returnOrder(o, at, cid, false)
		}
	}
	return nil
}

func (m *naiveModel) tick(at int) error {
	if err := m.advance(at); err != nil {
		return err
	}
	m.settle(at)
	return nil
}
