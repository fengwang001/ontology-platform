package staffingtest

func (m *Model) adjust(op Op) Result {
	if op.Pos == "" || op.Total < 0 || op.Now < 0 {
		return singleErr(CInvalidParam)
	}
	if !m.clockOK(op.Now) {
		return singleErr(CClockRollback)
	}
	if _, ok := m.positions[op.Pos]; !ok {
		return singleErr(CNotFound)
	}
	w := m.beginWork()
	w.settlePosition(op.Pos, op.Now, m.Grace)
	if op.Total < w.onboarded[op.Pos]+w.pending[op.Pos] {
		return singleErr(CFull)
	}
	m.positions[op.Pos].headcount = op.Total
	m.commitWork(w, op.Now, nil)
	return okResult()
}

func (m *Model) touchOfferWork(op Op) (*mOffer, *workState, Result) {
	if op.OfferID <= 0 || op.Now < 0 {
		return nil, nil, singleErr(CInvalidParam)
	}
	if !m.clockOK(op.Now) {
		return nil, nil, singleErr(CClockRollback)
	}
	w := m.beginWork()
	o, ok := w.offers[op.OfferID]
	if !ok {
		return nil, nil, singleErr(CNotFound)
	}
	w.settleOffer(o, op.Now, m.Grace)
	return o, w, Result{}
}

func (m *Model) respond(op Op) Result {
	if op.Accept && op.Entry < 0 {
		return singleErr(CInvalidParam)
	}
	o, w, r := m.touchOfferWork(op)
	if r.Code != 0 {
		return r
	}
	switch o.status {
	case MExpired, MAbandoned:
		return singleErr(CExpired)
	case MPending:
	default:
		return singleErr(CInvalidState)
	}
	if op.Accept && op.Entry < op.Now {
		return singleErr(CInvalidParam)
	}
	if op.Accept {
		o.status = MAccepted
		o.respondedAt = op.Now
		o.entryDate = op.Entry
		m.commitWork(w, op.Now, nil)
		return okResult()
	}
	o.status = MRejected
	o.respondedAt = op.Now
	w.pending[o.position]--
	delete(w.candPen, o.candidate)
	b := w.lastBlock[o.candidate]
	if b == nil {
		b = map[string]int{}
		w.lastBlock[o.candidate] = b
	}
	b[o.position] = op.Now
	m.commitWork(w, op.Now, nil)
	return okResult()
}

func (m *Model) onboard(op Op) Result {
	o, w, r := m.touchOfferWork(op)
	if r.Code != 0 {
		return r
	}
	switch o.status {
	case MExpired, MAbandoned:
		return singleErr(CExpired)
	case MAccepted:
	default:
		return singleErr(CInvalidState)
	}
	if op.Now < o.entryDate {
		return singleErr(CInvalidState)
	}
	o.status = MOnboarded
	o.onboardedAt = op.Now
	w.pending[o.position]--
	delete(w.candPen, o.candidate)
	w.onboarded[o.position]++
	w.candOnb[o.candidate] = o.id
	m.commitWork(w, op.Now, nil)
	return okResult()
}

func (m *Model) withdraw(op Op) Result {
	o, w, r := m.touchOfferWork(op)
	if r.Code != 0 {
		return r
	}
	switch o.status {
	case MExpired, MAbandoned:
		return singleErr(CExpired)
	case MPending:
		o.status = MWithdrawn
		w.pending[o.position]--
		delete(w.candPen, o.candidate)
		m.commitWork(w, op.Now, nil)
		return okResult()
	case MAccepted:
		return singleErr(CInvalidState)
	default:
		return singleErr(CInvalidState)
	}
}

func (m *Model) cancel(op Op) Result {
	o, w, r := m.touchOfferWork(op)
	if r.Code != 0 {
		return r
	}
	switch o.status {
	case MExpired, MAbandoned:
		return singleErr(CExpired)
	case MAccepted:
		o.status = MCanceled
		o.canceledAt = op.Now
		w.pending[o.position]--
		delete(w.candPen, o.candidate)
		m.commitWork(w, op.Now, nil)
		return okResult()
	case MPending:
		return singleErr(CInvalidState)
	default:
		return singleErr(CInvalidState)
	}
}

func (m *Model) leave(op Op) Result {
	if op.Cand == "" || op.Now < 0 {
		return singleErr(CInvalidParam)
	}
	if !m.clockOK(op.Now) {
		return singleErr(CClockRollback)
	}
	w := m.beginWork()
	id, ok := w.candOnb[op.Cand]
	if !ok {
		return singleErr(CInvalidState)
	}
	o := w.offers[id]
	if o.status != MOnboarded || w.onboarded[o.position] <= 0 {
		return singleErr(CInvalidState)
	}
	o.leftAt = op.Now
	w.onboarded[o.position]--
	delete(w.candOnb, op.Cand)
	m.commitWork(w, op.Now, nil)
	return okResult()
}

// touch 对应 GetOffer / Occupancy / Snapshot：查询本身是被接受操作，
// 因此惰性结算被物化并推进时钟。
func (m *Model) touch(op Op) Result {
	if op.Now < 0 {
		return singleErr(CInvalidParam)
	}
	if !m.clockOK(op.Now) {
		return singleErr(CClockRollback)
	}
	if op.Kind == "GetOffer" {
		if op.OfferID <= 0 {
			return singleErr(CInvalidParam)
		}
		if _, ok := m.offers[op.OfferID]; !ok {
			return singleErr(CNotFound)
		}
		w := m.beginWork()
		w.settleOffer(w.offers[op.OfferID], op.Now, m.Grace)
		m.commitWork(w, op.Now, nil)
		return okResult()
	}
	if op.Pos == "" {
		return singleErr(CInvalidParam)
	}
	if _, ok := m.positions[op.Pos]; !ok {
		return singleErr(CNotFound)
	}
	w := m.beginWork()
	w.settlePosition(op.Pos, op.Now, m.Grace)
	m.commitWork(w, op.Now, nil)
	return okResult()
}
