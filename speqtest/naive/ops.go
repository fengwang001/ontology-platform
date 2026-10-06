package naive

import "sort"

func mkErr(c Code, m string) error { return &Err{Code: c, Msg: m} }

func (m *Model) checkDate(date int) error {
	if date < m.Last {
		return mkErr(CodeRegression, "regression")
	}
	return nil
}

func (m *Model) AddCategory(c Category) error {
	if c.Code == "" || c.PeriodM <= 0 || c.WindowD < 0 || c.MinUnseal < 0 || c.WarnLead < 0 {
		return mkErr(CodeInvalid, "bad category")
	}
	m.Cats[c.Code] = c
	return nil
}

func (m *Model) register(date int, id, cat string, k Kind, first int) (int, error) {
	if date < 0 || first < 0 || id == "" || first > date {
		return 0, mkErr(CodeInvalid, "bad param")
	}
	if e := m.checkDate(date); e != nil {
		return 0, e
	}
	c, ok := m.Cats[cat]
	if !ok {
		return 0, mkErr(CodeNotFound, "no category")
	}
	if c.Kind != k {
		return 0, mkErr(CodeState, "kind mismatch")
	}
	if _, dup := m.Objs[id]; dup {
		return 0, mkErr(CodeState, "dup id")
	}
	exp := addMonths(first, c.PeriodM)
	m.Objs[id] = &O{ID: id, Cat: cat, Kind: k, Exp: exp}
	m.Last = date
	return exp, nil
}

func (m *Model) RegisterDevice(date int, id, cat string, first int) (int, error) {
	return m.register(date, id, cat, KDevice, first)
}

func (m *Model) RegisterAttachment(date int, id, cat string, k Kind, first int) (int, error) {
	if k != KSV && k != KPG {
		return 0, mkErr(CodeInvalid, "bad kind")
	}
	return m.register(date, id, cat, k, first)
}

func (m *Model) Inspect(date int, id string, r Result, rectify int) (int, error) {
	if date < 0 || id == "" || r < RPass || r > RFail || rectify < 0 {
		return 0, mkErr(CodeInvalid, "bad param")
	}
	if e := m.checkDate(date); e != nil {
		return 0, e
	}
	o, ok := m.Objs[id]
	if !ok {
		return 0, mkErr(CodeNotFound, "no obj")
	}
	if o.Scrap {
		return 0, mkErr(CodeScrapped, "scrapped")
	}
	c := m.Cats[o.Cat]
	if r == RFail {
		o.Dis = true
		if o.Seal {
			o.SealAnchor = date
		}
		m.Last = date
		return o.Exp, nil
	}
	basis := addMonths(date, c.PeriodM)
	if date <= o.Exp && o.Exp-date <= c.WindowD {
		basis = addMonths(o.Exp, c.PeriodM)
	}
	ne := basis
	if r == RCond && date+rectify < ne {
		ne = date + rectify
	}
	o.Exp = ne
	if r == RPass {
		o.Dis = false
	}
	if o.Seal {
		o.SealAnchor = date
	}
	m.Last = date
	return ne, nil
}

func (m *Model) Seal(date int, id string) error {
	if date < 0 || id == "" {
		return mkErr(CodeInvalid, "bad param")
	}
	if e := m.checkDate(date); e != nil {
		return e
	}
	o, ok := m.Objs[id]
	if !ok {
		return mkErr(CodeNotFound, "no obj")
	}
	if o.Scrap {
		return mkErr(CodeScrapped, "scrapped")
	}
	if o.Seal {
		return mkErr(CodeState, "already sealed")
	}
	if o.Dis {
		return mkErr(CodeState, "disabled")
	}
	if date > o.Exp {
		return mkErr(CodeCondition, "expired")
	}
	o.Seal = true
	o.SealDate = date
	o.SealAnchor = date
	m.Last = date
	return nil
}

func (m *Model) Unseal(date int, id string) (int, error) {
	if date < 0 || id == "" {
		return 0, mkErr(CodeInvalid, "bad param")
	}
	if e := m.checkDate(date); e != nil {
		return 0, e
	}
	o, ok := m.Objs[id]
	if !ok {
		return 0, mkErr(CodeNotFound, "no obj")
	}
	if o.Scrap {
		return 0, mkErr(CodeScrapped, "scrapped")
	}
	if !o.Seal {
		return 0, mkErr(CodeState, "not sealed")
	}
	elapsed := date - o.SealAnchor
	if elapsed < 0 {
		elapsed = 0
	}
	cand := o.Exp + elapsed
	c := m.Cats[o.Cat]
	if cand-date < c.MinUnseal {
		return 0, mkErr(CodeCondition, "min unseal")
	}
	o.Exp = cand
	o.Seal = false
	o.SealDate = 0
	o.SealAnchor = 0
	m.Last = date
	return o.Exp, nil
}

func (m *Model) attachmentsOfLocked(did string) []string {
	var ids []string
	for aid, host := range m.HostOf {
		if host == did {
			ids = append(ids, aid)
		}
	}
	sort.Strings(ids)
	return ids
}

func (m *Model) Mount(date int, aid, did string) error {
	if date < 0 || aid == "" || did == "" {
		return mkErr(CodeInvalid, "bad param")
	}
	if e := m.checkDate(date); e != nil {
		return e
	}
	a, ok := m.Objs[aid]
	if !ok {
		return mkErr(CodeNotFound, "no attach")
	}
	if a.Scrap {
		return mkErr(CodeScrapped, "scrapped")
	}
	if a.Kind != KSV && a.Kind != KPG {
		return mkErr(CodeState, "not attach")
	}
	d, ok := m.Objs[did]
	if !ok {
		return mkErr(CodeNotFound, "no device")
	}
	if d.Scrap {
		return mkErr(CodeScrapped, "scrapped")
	}
	if d.Kind != KDevice {
		return mkErr(CodeState, "not device")
	}
	if a.Seal {
		return mkErr(CodeState, "attach sealed")
	}
	if a.Dis {
		return mkErr(CodeCondition, "attach disabled")
	}
	if date > a.Exp {
		return mkErr(CodeCondition, "attach expired")
	}
	m.HostOf[aid] = did
	m.Last = date
	return nil
}

func (m *Model) Scrap(date int, id string) error {
	if date < 0 || id == "" {
		return mkErr(CodeInvalid, "bad param")
	}
	if e := m.checkDate(date); e != nil {
		return e
	}
	o, ok := m.Objs[id]
	if !ok {
		return mkErr(CodeNotFound, "no obj")
	}
	if o.Scrap {
		return mkErr(CodeScrapped, "scrapped")
	}
	if o.Kind == KDevice {
		for aid, host := range m.HostOf {
			if host == id {
				delete(m.HostOf, aid)
			}
		}
	} else {
		delete(m.HostOf, id)
	}
	o.Scrap = true
	o.Seal = false
	o.Dis = false
	m.Last = date
	return nil
}

func (m *Model) usable(d *O, date int) (Reject, string, string) {
	if d.Seal {
		return RejectSealed, "", "sealed"
	}
	if d.Dis {
		return RejectDisabled, "", "disabled"
	}
	if date > d.Exp {
		return RejectExpired, "", "expired"
	}
	hasSV := false
	var bad, detail string
	for _, aid := range m.attachmentsOfLocked(d.ID) {
		a := m.Objs[aid]
		if a == nil || a.Scrap {
			continue
		}
		if a.Kind == KSV {
			hasSV = true
		}
		if bad == "" {
			switch {
			case a.Seal:
				bad, detail = aid, "sealed"
			case a.Dis:
				bad, detail = aid, "disabled"
			case date > a.Exp:
				bad, detail = aid, "expired"
			}
		}
	}
	if !hasSV {
		return RejectNoSV, "", "no sv"
	}
	if bad != "" {
		return RejectAttach, bad, detail
	}
	return RejectOK, "", ""
}

func (m *Model) RegisterUse(date int, did string) error {
	if date < 0 || did == "" {
		return mkErr(CodeInvalid, "bad param")
	}
	if e := m.checkDate(date); e != nil {
		return e
	}
	d, ok := m.Objs[did]
	if !ok {
		return mkErr(CodeNotFound, "no device")
	}
	if d.Scrap {
		return mkErr(CodeScrapped, "scrapped")
	}
	if d.Kind != KDevice {
		return mkErr(CodeState, "not device")
	}
	r, aid, det := m.usable(d, date)
	if r != RejectOK {
		return &UseError{Reason: r, Attach: aid, Detail: det}
	}
	m.Last = date
	return nil
}

func (m *Model) Snapshot() []Snap {
	ids := make([]string, 0, len(m.Objs))
	for id := range m.Objs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := []Snap{}
	for _, id := range ids {
		o := m.Objs[id]
		out = append(out, Snap{
			ID: o.ID, Cat: o.Cat, Kind: o.Kind,
			Scrap: o.Scrap, Seal: o.Seal, Dis: o.Dis,
			Exp: o.Exp, Host: m.HostOf[id],
		})
	}
	return out
}

func (m *Model) LastDate() int { return m.Last }
