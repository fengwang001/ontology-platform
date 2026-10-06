package greenreg

import "sort"

// naiveModel is an independent reference implementation. It deliberately uses
// only plain slices/maps, rescans every certificate for each decision and
// commits by "snapshot -> tentative mutate -> rollback on first error". It
// shares no logic with Registry beyond types and the error taxonomy.
type naiveModel struct {
	unit   int64
	maxAge int64

	facils map[string]*naiveFacility
	certs  []*naiveCert
	usage  map[string]map[int64]*naiveUsage
	events []Event
	clock  int64
}

type naiveFacility struct {
	holder  string
	start   int64
	end     int64
	hasEnd  bool
	balance int64
	meters  map[int64]*naiveMeter
	order   []int64
}

type naiveMeter struct {
	qty, in, remain int64
}

type naiveCert struct {
	serial     int64
	facility   string
	generation int64
	holder     string
	status     int
	retUser    string
	usage      int64
	retSeq     int64
}

type naiveUsage struct {
	qty, units int64
	active     int
}

// NewNaive builds the reference model with the same config.
func NewNaive(cfg Config) *naiveModel {
	if cfg.UnitQty <= 0 {
		cfg.UnitQty = 1
	}
	if cfg.MaxAgePeriods < 0 {
		cfg.MaxAgePeriods = 0
	}
	return &naiveModel{
		unit:   cfg.UnitQty,
		maxAge: cfg.MaxAgePeriods,
		facils: make(map[string]*naiveFacility),
		usage:  make(map[string]map[int64]*naiveUsage),
	}
}

func (m *naiveModel) clone() *naiveModel {
	cp := &naiveModel{unit: m.unit, maxAge: m.maxAge, clock: m.clock}
	cp.facils = make(map[string]*naiveFacility, len(m.facils))
	for id, f := range m.facils {
		nf := &naiveFacility{
			holder: f.holder, start: f.start, end: f.end, hasEnd: f.hasEnd,
			balance: f.balance, order: append([]int64(nil), f.order...),
			meters: make(map[int64]*naiveMeter, len(f.meters)),
		}
		for p, mm := range f.meters {
			nf.meters[p] = &naiveMeter{qty: mm.qty, in: mm.in, remain: mm.remain}
		}
		cp.facils[id] = nf
	}
	cp.certs = make([]*naiveCert, len(m.certs))
	for i, c := range m.certs {
		cc := *c
		cp.certs[i] = &cc
	}
	cp.usage = make(map[string]map[int64]*naiveUsage, len(m.usage))
	for u, ps := range m.usage {
		cp.usage[u] = make(map[int64]*naiveUsage, len(ps))
		for p, up := range ps {
			cp.usage[u][p] = &naiveUsage{qty: up.qty, units: up.units, active: up.active}
		}
	}
	cp.events = append([]Event(nil), m.events...)
	return cp
}

func (m *naiveModel) cert(s int64) *naiveCert {
	if s < 1 || s > int64(len(m.certs)) {
		return nil
	}
	return m.certs[s-1]
}

func (m *naiveModel) RegisterFacility(id, holder string, start int64) error {
	if id == "" || holder == "" || start < 0 {
		return newErr(ErrInvalid, "参数非法")
	}
	if _, ok := m.facils[id]; ok {
		return newErr(ErrInvalid, "设施已存在")
	}
	m.facils[id] = &naiveFacility{holder: holder, start: start, meters: map[int64]*naiveMeter{}}
	return nil
}

func (m *naiveModel) SetTermination(id string, end int64) error {
	if id == "" {
		return newErr(ErrInvalid, "参数非法")
	}
	f, ok := m.facils[id]
	if !ok {
		return newErr(ErrInvalid, "设施不存在")
	}
	if end < 0 {
		return newErr(ErrInvalid, "终止期编号越界")
	}
	if f.hasEnd && end != 0 && end < f.end {
		return newErr(ErrInvalid, "资格终止期不可提前")
	}
	if end != 0 && end <= f.start {
		return newErr(ErrInvalid, "终止期须晚于生效期")
	}
	if end != 0 {
		for _, c := range m.certs {
			if c.facility == id && c.status != StatusRevoked && c.generation >= end {
				return newErr(ErrConflictIssued, "终止期会使已核发证书落到资格区间之外")
			}
		}
	}
	if end == 0 {
		f.hasEnd, f.end = false, 0
	} else {
		f.hasEnd, f.end = true, end
	}
	return nil
}

func (m *naiveModel) Transfer(from, to string, serials []int64) error {
	if from == "" || to == "" || from == to || len(serials) == 0 {
		return newErr(ErrInvalid, "参数非法/自转")
	}
	seen := map[int64]bool{}
	sorted := append([]int64(nil), serials...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, s := range sorted {
		if s <= 0 {
			return newErr(ErrInvalid, "序号不存在")
		}
		if seen[s] {
			return newErr(ErrInvalid, "批内序号重复")
		}
		seen[s] = true
	}
	for _, s := range sorted {
		c := m.cert(s)
		if c == nil {
			return failErr(ErrInvalid, "序号不存在", s)
		}
		if c.status != StatusHeld {
			return failErr(ErrStateNotAllowed, "证书非持有状态", s)
		}
		if c.holder != from {
			return failErr(ErrNotHolder, "非持有人", s)
		}
	}
	for _, s := range sorted {
		c := m.cert(s)
		c.holder = to
		m.events = append(m.events, Event{Kind: EvTransferred, Cert: s, From: from, To: to})
	}
	return nil
}

func (m *naiveModel) RegisterUsage(user string, period, qty int64) error {
	if user == "" || period < 0 || qty < 0 {
		return newErr(ErrInvalid, "参数非法")
	}
	if m.usage[user] == nil {
		m.usage[user] = map[int64]*naiveUsage{}
	}
	u := m.usage[user][period]
	if u == nil {
		u = &naiveUsage{}
		m.usage[user][period] = u
	}
	if qty < u.units {
		return newErr(ErrBelowRetired, "用电量修正低于已注销量")
	}
	u.qty = qty
	return nil
}

func (m *naiveModel) RegisterGeneration(id string, period, qty int64) (issued, revoked []int64, retErr error) {
	if id == "" || period < 0 || qty < 0 {
		return nil, nil, newErr(ErrInvalid, "参数非法")
	}
	f, ok := m.facils[id]
	if !ok {
		return nil, nil, newErr(ErrInvalid, "设施不存在")
	}
	mm, exists := f.meters[period]
	if !exists && qty == 0 {
		return nil, nil, newErr(ErrInvalid, "新登记电量须为正")
	}
	if period < f.start || (f.hasEnd && period >= f.end) {
		return nil, nil, newErr(ErrEligibility, "发电期不在资格区间内")
	}

	saved := m.clone()
	defer func() {
		if retErr != nil {
			*m = *saved
		}
	}()

	input, live := f.balance, int64(0)
	if exists {
		input = mm.in
	}
	for _, c := range m.certs {
		if c.facility == id && c.generation == period && c.status != StatusRevoked {
			live++
		}
	}
	want := (qty + input) / m.unit
	remain := (qty + input) % m.unit

	for live+int64(len(issued)) < want {
		c := &naiveCert{
			serial: int64(len(m.certs)) + 1, facility: id, generation: period,
			holder: f.holder, status: StatusHeld,
		}
		m.certs = append(m.certs, c)
		issued = append(issued, c.serial)
		m.events = append(m.events, Event{Kind: EvIssued, Cert: c.serial, Facility: id, Generation: period, Holder: f.holder})
	}

	if live > want {
		n := live - want
		var held []*naiveCert
		for _, c := range m.certs {
			if c.facility == id && c.generation == period && c.status == StatusHeld {
				held = append(held, c)
			}
		}
		sort.Slice(held, func(i, j int) bool { return held[i].serial > held[j].serial })
		for _, c := range held {
			if n == 0 {
				break
			}
			c.status = StatusRevoked
			revoked = append(revoked, c.serial)
			m.events = append(m.events, Event{Kind: EvRevoked, Cert: c.serial, Facility: id, Generation: period})
			n--
		}
		if n > 0 {
			var ret []*naiveCert
			for _, c := range m.certs {
				if c.facility == id && c.generation == period && c.status == StatusRetired {
					ret = append(ret, c)
				}
			}
			sort.Slice(ret, func(i, j int) bool { return ret[i].retSeq > ret[j].retSeq })
			for _, c := range ret {
				if n == 0 {
					break
				}
				user, up := c.retUser, c.usage
				uu := m.usage[user][up]
				uu.units -= m.unit
				uu.active--
				c.status = StatusRevoked
				revoked = append(revoked, c.serial)
				m.events = append(m.events, Event{Kind: EvRevoked, Cert: c.serial, Facility: id, Generation: period})
				m.events = append(m.events, Event{Kind: EvDeclarationVoided, Cert: c.serial, User: user, UsagePeriod: up})
				n--
			}
		}
	}

	if exists {
		mm.qty = qty
		mm.remain = remain
	} else {
		f.meters[period] = &naiveMeter{qty: qty, in: input, remain: remain}
		f.order = append(f.order, period)
	}
	if len(f.order) > 0 && f.order[len(f.order)-1] == period {
		f.balance = remain
	}
	return issued, revoked, nil
}

func (m *naiveModel) Retire(user string, period int64, serials []int64) error {
	if user == "" || len(serials) == 0 || period < 0 {
		return newErr(ErrInvalid, "参数非法")
	}
	u := m.usage[user][period]
	if u == nil {
		return newErr(ErrInvalid, "用电期未登记用电量")
	}
	seen := map[int64]bool{}
	sorted := append([]int64(nil), serials...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var chosen []*naiveCert
	for _, s := range sorted {
		if s <= 0 {
			return newErr(ErrInvalid, "序号不存在")
		}
		if seen[s] {
			return newErr(ErrInvalid, "批内序号重复")
		}
		seen[s] = true
	}
	for _, s := range sorted {
		c := m.cert(s)
		if c == nil {
			return failErr(ErrInvalid, "序号不存在", s)
		}
		if c.status != StatusHeld {
			return failErr(ErrStateNotAllowed, "证书非持有状态", s)
		}
		if c.holder != user {
			return failErr(ErrNotHolder, "非持有人", s)
		}
		if c.generation > period || c.generation < period-m.maxAge {
			return failErr(ErrPeriodMismatch, "发电期超出允许期限", s)
		}
		chosen = append(chosen, c)
	}
	if u.units+int64(len(chosen))*m.unit > u.qty {
		return failErr(ErrOverUsage, "注销总量超过用电量", chosen[0].serial)
	}
	for _, c := range chosen {
		m.clock++
		c.status = StatusRetired
		c.retUser = user
		c.usage = period
		c.retSeq = m.clock
		u.units += m.unit
		u.active++
		m.events = append(m.events, Event{Kind: EvRetired, Cert: c.serial, User: user, UsagePeriod: period})
	}
	return nil
}

// Snapshot builds the shared canonical representation from scratch.
func (m *naiveModel) Snapshot() string {
	st := &CanonicalState{Events: m.events}
	for _, id := range sortedKeys(m.facils) {
		f := m.facils[id]
		cf := CanonicalFacility{ID: id, Holder: f.holder, Start: f.start, End: f.end, HasEnd: f.hasEnd, Balance: f.balance}
		ps := append([]int64(nil), f.order...)
		sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
		for _, p := range ps {
			var live int64
			for _, c := range m.certs {
				if c.facility == id && c.generation == p && c.status != StatusRevoked {
					live++
				}
			}
			cf.Meters = append(cf.Meters, CanonicalMeter{Period: p, Qty: f.meters[p].qty, Remain: f.balance, Live: live})
			cf.Meters[len(cf.Meters)-1].Remain = f.meters[p].remain
		}
		st.Facilities = append(st.Facilities, cf)
	}
	for _, c := range m.certs {
		st.Certs = append(st.Certs, &Certificate{
			Serial: c.serial, Facility: c.facility, Generation: c.generation, Holder: c.holder,
			Status: c.status, RetireUser: c.retUser, UsagePeriod: c.usage, RetireSeq: c.retSeq,
		})
	}
	for _, u := range sortedKeys(m.usage) {
		ps := make([]int64, 0, len(m.usage[u]))
		for p := range m.usage[u] {
			ps = append(ps, p)
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
		for _, p := range ps {
			uu := m.usage[u][p]
			st.Usage = append(st.Usage, CanonicalUsage{User: u, Period: p, Qty: uu.qty, Units: uu.units, Active: uu.active})
		}
	}
	return renderCanonical(st)
}
