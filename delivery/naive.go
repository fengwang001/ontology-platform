package delivery

// naEvent is one accepted operation kept in the immutable operation log.
type naEvent struct {
	kind    string
	orderID string
	t       int
	addr    Address
	disp    Disposition
	evID    string
	evTime  int
}

// naState is one order reconstructed purely from the event prefix.
type naState struct {
	id        string
	status    OrderStatus
	address   Address
	disp      Disposition
	liability Liability
	changes   int
	comp      bool
	seq       int
	curTyp    ExceptionType
	curPhase  ExceptionPhase
	curStart  int
	curCnt    int
	curLast   int
	curEnd    int
	curEvID   string
	curEvTime int
	windowEnd int
}

func (s *naState) toOrder() *Order {
	o := &Order{
		ID:           s.id,
		Status:       s.status,
		Address:      s.address,
		Disposition:  s.disp,
		Liability:    s.liability,
		AddrChanges:  s.changes,
		CompPaid:     s.comp,
		ExceptionSeq: s.seq,
		WindowEnd:    s.windowEnd,
	}
	if s.curPhase != 0 {
		o.Current = &Exception{
			ID:           s.seq,
			Type:         s.curTyp,
			Phase:        s.curPhase,
			Start:        s.curStart,
			ContactCount: s.curCnt,
			WindowEnd:    s.curEnd,
			EvidenceID:   s.curEvID,
			EvidenceTime: s.curEvTime,
		}
	}
	return o
}

// NaiveModel is an independently written reference model. It keeps the
// accepted operation log and rebuilds an order's state from scratch on
// every call, so its correctness does not rely on Platform lazy updates.
type NaiveModel struct {
	cfg  Config
	log  []naEvent
	maxT int
}

// NewNaiveModel constructs the reference model.
func NewNaiveModel(cfg Config) (*NaiveModel, error) {
	if cfg.MinWaitSeconds < 0 || cfg.MinContacts < 0 || cfg.MinContactInterval < 0 ||
		cfg.CorrectionWindow < 0 || cfg.MaxCorrectionDist < 0 ||
		cfg.EvidenceTTLSeconds < 0 || cfg.RiderCompensation < 0 ||
		cfg.MerchantConfirmWindow < 0 {
		return nil, newError(ErrInvalidParam, "config values must be non-negative")
	}
	return &NaiveModel{cfg: cfg}, nil
}

// mature applies windows of s that are due no later than observation
// time t. Written independently from Platform.applyTimeEffects and working
// from event-derived state rather than lazily maintained endpoints.
func (m *NaiveModel) mature(s *naState, t int) {
	for {
		if s.status == OrderAwaitingCorrection && t >= s.windowEnd {
			s.curPhase = ExceptionUndeliverable
			m.becomeUndeliverable(s, LiabilityCustomer)
			continue
		}
		if s.status == OrderReturning && s.windowEnd != 0 && t >= s.windowEnd {
			s.status = OrderReturnUnconfirmed
			s.liability = LiabilityMerchant
			s.curPhase = 0
			s.windowEnd = 0
			continue
		}
		return
	}
}

// becomeUndeliverable applies liability, compensation and disposition.
func (m *NaiveModel) becomeUndeliverable(s *naState, liable Liability) {
	s.liability = liable
	if !s.comp {
		s.comp = true
	}
	if s.disp == DispositionReturn {
		s.status = OrderReturning
		s.windowEnd = 0
	} else {
		s.status = OrderDisposed
		s.curPhase = 0
	}
}

// replay rebuilds the given order's state from the complete log prefix.
// Every stored event is folded together with the maturation due at its
// own timestamp, mirroring how each accepted Platform operation observed
// the world when it happened.
func (m *NaiveModel) replay(id string) *naState {
	var s *naState
	for i := range m.log {
		e := &m.log[i]
		if e.orderID != id {
			continue
		}
		if s == nil {
			if e.kind != "create" {
				continue
			}
			s = &naState{id: id, status: OrderCreated, address: e.addr, disp: e.disp}
			continue
		}
		// Apply every window effect matured by this event's timestamp
		// before folding the event itself.
		m.mature(s, e.t)
		m.apply(s, e)
	}
	return s
}

// apply folds a single event into the reconstructed state.
func (m *NaiveModel) apply(s *naState, e *naEvent) {
	switch e.kind {
	case "create":
		// state was seeded by replay.
	case "pickup":
		if s.status == OrderCreated {
			s.status = OrderDelivering
		}
	case "deliver":
		if s.status == OrderDelivering {
			s.status = OrderDelivered
		}
	case "report-unreachable":
		if s.status == OrderDelivering {
			m.openNew(s, ExceptionUnreachable, e.t)
			s.curEnd = 0
		}
	case "report-wrong":
		if s.status == OrderDelivering {
			m.openNew(s, ExceptionWrongAddress, e.t)
			s.curEnd = e.t + m.cfg.CorrectionWindow
			s.status = OrderAwaitingCorrection
			s.windowEnd = s.curEnd
		}
	case "report-refusal":
		if s.status == OrderDelivering {
			m.openNew(s, ExceptionRefusal, e.t)
			s.curPhase = ExceptionUndeliverable
			s.curEvID = e.evID
			s.curEvTime = e.evTime
			m.becomeUndeliverable(s, LiabilityCustomer)
		}
	case "contact":
		if s.status == OrderDelivering && s.curPhase == ExceptionOpen &&
			s.curTyp == ExceptionUnreachable {
			s.curCnt++
			s.curLast = e.t
		}
	case "respond":
		if s.status == OrderDelivering && s.curPhase == ExceptionOpen &&
			s.curTyp == ExceptionUnreachable {
			s.curPhase = ExceptionClosed
		}
	case "judge":
		if s.status == OrderDelivering && s.curPhase == ExceptionOpen &&
			s.curTyp == ExceptionUnreachable &&
			e.t-s.curStart >= m.cfg.MinWaitSeconds &&
			s.curCnt >= m.cfg.MinContacts {
			s.curPhase = ExceptionUndeliverable
			m.becomeUndeliverable(s, LiabilityCustomer)
		}
	case "correct":
		if s.status == OrderAwaitingCorrection && s.curPhase == ExceptionOpen &&
			e.t < s.windowEnd {
			s.address = e.addr
			s.changes++
			s.curPhase = ExceptionClosed
			s.status = OrderDelivering
			s.windowEnd = 0
		}
	case "return":
		if s.status == OrderReturning && s.windowEnd == 0 {
			s.windowEnd = e.t + m.cfg.MerchantConfirmWindow
		}
	case "confirm":
		if s.status == OrderReturning && s.windowEnd != 0 && e.t < s.windowEnd {
			s.status = OrderReturned
			s.windowEnd = 0
			s.curPhase = 0
		}
	}
}

// openNew starts a fresh exception: a new report always counts from zero.
func (m *NaiveModel) openNew(s *naState, typ ExceptionType, t int) {
	s.seq++
	s.curTyp = typ
	s.curPhase = ExceptionOpen
	s.curStart = t
	s.curCnt = 0
	s.curLast = 0
	s.curEnd = 0
	s.curEvID = ""
	s.curEvTime = 0
}

// effectiveStatus mirrors Platform.effectiveStatus on the replay state.
func (m *NaiveModel) effectiveStatus(s *naState, t int) OrderStatus {
	switch s.status {
	case OrderAwaitingCorrection:
		if t >= s.windowEnd {
			if s.disp == DispositionReturn {
				return OrderReturning
			}
			return OrderDisposed
		}
	case OrderReturning:
		if s.windowEnd != 0 && t >= s.windowEnd {
			return OrderReturnUnconfirmed
		}
	}
	return s.status
}

func (m *NaiveModel) hasOpen(s *naState, t int) bool {
	if s.curPhase != ExceptionOpen {
		return false
	}
	if s.status == OrderAwaitingCorrection && t >= s.windowEnd {
		return false
	}
	return true
}

// begin resolves clock and order, returning the stored replay state plus
// its matured view at t. Nothing is persisted by a rejected operation.
func (m *NaiveModel) begin(id string, t int) (*naState, *naState, error) {
	if t < m.maxT {
		return nil, nil, newError(ErrClockBack, "clock regression")
	}
	stored := m.replay(id)
	if stored == nil {
		return nil, nil, newError(ErrOrderNotFound, "order not found: "+id)
	}
	view := m.clone(stored)
	m.mature(view, t)
	return stored, view, nil
}

func (m *NaiveModel) clone(s *naState) *naState {
	cp := *s
	return &cp
}

// accept stores the event. The rebuilt state already includes maturation
// at prior events; the new event is appended and matured/applied at t.
func (m *NaiveModel) accept(e naEvent, view *naState, t int) *Order {
	e.t = t
	m.log = append(m.log, e)
	if t > m.maxT {
		m.maxT = t
	}
	return view.toOrder()
}

// CreateOrder places an order at time t.
func (m *NaiveModel) CreateOrder(id string, t int, addr Address, disp Disposition) (*Order, error) {
	if id == "" || (disp != DispositionReturn && disp != DispositionOnSite) {
		return nil, newError(ErrInvalidParam, "invalid order id or disposition")
	}
	if t < m.maxT {
		return nil, newError(ErrClockBack, "clock regression")
	}
	if s := m.replay(id); s != nil {
		return nil, newError(ErrInvalidParam, "duplicate order id: "+id)
	}
	s := &naState{id: id, status: OrderCreated, address: addr, disp: disp}
	return m.accept(naEvent{kind: "create", orderID: id, addr: addr, disp: disp}, s, t), nil
}

// Pickup marks an existing order as picked up.
func (m *NaiveModel) Pickup(id string, t int) (*Order, error) {
	_, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	if v.status != OrderCreated {
		return nil, newError(ErrInvalidParam, "pickup requires created order")
	}
	m.apply(v, &naEvent{kind: "pickup", orderID: id, t: t})
	return m.accept(naEvent{kind: "pickup", orderID: id}, v, t), nil
}

// Deliver marks an order delivered.
func (m *NaiveModel) Deliver(id string, t int) (*Order, error) {
	_, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	switch v.status {
	case OrderDelivering:
		if m.hasOpen(v, t) {
			return nil, newError(ErrActiveException, "an exception is in progress")
		}
		m.apply(v, &naEvent{kind: "deliver", orderID: id, t: t})
		return m.accept(naEvent{kind: "deliver", orderID: id}, v, t), nil
	case OrderCreated:
		return nil, newError(ErrNotPickedUp, "order not picked up")
	case OrderDelivered:
		return nil, newError(ErrAlreadyDelivered, "order already delivered")
	default:
		return nil, newError(naStatusCode(v.status), "deliver invalid in current status")
	}
}

func naStatusCode(s OrderStatus) ErrorCode {
	switch s {
	case OrderAwaitingCorrection, OrderReturning:
		return ErrActiveException
	case OrderReturned, OrderReturnUnconfirmed, OrderDisposed:
		return ErrOrderTerminal
	default:
		return ErrTypeMismatch
	}
}

// reportViewPre validates report preconditions against the matured view.
func (m *NaiveModel) reportViewPre(v *naState, t int) error {
	switch v.status {
	case OrderCreated:
		return newError(ErrNotPickedUp, "cannot report before pickup")
	case OrderDelivered:
		return newError(ErrAlreadyDelivered, "cannot report after delivery")
	case OrderReturned, OrderReturnUnconfirmed, OrderDisposed:
		return newError(ErrOrderTerminal, "order already terminal")
	case OrderAwaitingCorrection, OrderReturning:
		return newError(ErrActiveException, "an exception is already in progress")
	case OrderDelivering:
		if m.hasOpen(v, t) {
			return newError(ErrActiveException, "an exception is already in progress")
		}
	}
	return nil
}

// ReportUnreachable opens an unreachable exception.
func (m *NaiveModel) ReportUnreachable(id string, t int) (*Exception, error) {
	_, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	if err := m.reportViewPre(v, t); err != nil {
		return nil, err
	}
	e := naEvent{kind: "report-unreachable", orderID: id}
	m.apply(v, &naEvent{kind: e.kind, orderID: id, t: t})
	o := m.accept(e, v, t)
	return o.Current, nil
}

// ReportWrongAddress opens a wrong-address exception.
func (m *NaiveModel) ReportWrongAddress(id string, t int) (*Exception, error) {
	_, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	if err := m.reportViewPre(v, t); err != nil {
		return nil, err
	}
	e := naEvent{kind: "report-wrong", orderID: id}
	m.apply(v, &naEvent{kind: e.kind, orderID: id, t: t})
	o := m.accept(e, v, t)
	return o.Current, nil
}

// ReportRefusal opens a refusal exception with evidence.
func (m *NaiveModel) ReportRefusal(id string, t int, evidenceID string, evidenceTime int) (*Exception, error) {
	if evidenceID == "" {
		return nil, newError(ErrInvalidParam, "evidence id required")
	}
	_, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	if err := m.reportViewPre(v, t); err != nil {
		return nil, err
	}
	if evidenceTime < t-m.cfg.EvidenceTTLSeconds {
		return nil, newError(ErrEvidenceInvalid, "refusal evidence out of validity window")
	}
	e := naEvent{kind: "report-refusal", orderID: id, evID: evidenceID, evTime: evidenceTime}
	m.apply(v, &naEvent{kind: e.kind, orderID: id, t: t, evID: evidenceID, evTime: evidenceTime})
	order := m.accept(e, v, t)
	if order.Current != nil {
		return order.Current, nil
	}
	// On-site disposition clears the current exception, but the refusal
	// report itself still returns its established exception snapshot.
	return &Exception{
		ID: v.seq, Type: ExceptionRefusal, Phase: ExceptionUndeliverable,
		Start: t, EvidenceID: evidenceID, EvidenceTime: evidenceTime,
	}, nil
}

// contactViewPre resolves an unreachable-only action on the matured view.
func (m *NaiveModel) contactViewPre(v *naState, t int) error {
	if v.curTyp == ExceptionUnreachable && v.curPhase == ExceptionOpen && v.status == OrderDelivering {
		return nil
	}
	switch {
	case v.status == OrderReturnUnconfirmed || v.status == OrderReturned || v.status == OrderDisposed:
		return newError(ErrOrderTerminal, "order already terminal")
	case v.status == OrderReturning || v.status == OrderAwaitingCorrection:
		return newError(ErrActiveException, "an exception is already in progress")
	case v.curPhase == 0:
		return newError(ErrExceptionNotFound, "no in-progress exception")
	case v.curPhase != ExceptionOpen:
		return newError(ErrExceptionClosed, "exception already closed")
	default:
		return newError(ErrTypeMismatch, "open exception is not unreachable")
	}
}

// RecordContact logs one contact attempt.
func (m *NaiveModel) RecordContact(id string, t int) (*Exception, error) {
	_, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	if err := m.contactViewPre(v, t); err != nil {
		return nil, err
	}
	if v.curCnt > 0 && t-v.curLast < m.cfg.MinContactInterval {
		return nil, newError(ErrContactTooFrequent, "contact attempts too frequent")
	}
	e := naEvent{kind: "contact", orderID: id}
	m.apply(v, &naEvent{kind: "contact", orderID: id, t: t})
	return m.accept(e, v, t).Current, nil
}

// CustomerRespond closes an open unreachable exception.
func (m *NaiveModel) CustomerRespond(id string, t int) (*Order, error) {
	_, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	if err := m.contactViewPre(v, t); err != nil {
		return nil, err
	}
	e := naEvent{kind: "respond", orderID: id}
	m.apply(v, &naEvent{kind: "respond", orderID: id, t: t})
	return m.accept(e, v, t), nil
}

// JudgeUndeliverable attempts to pronounce an unreachable order undeliverable.
func (m *NaiveModel) JudgeUndeliverable(id string, t int) (*Order, error) {
	_, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	if err := m.contactViewPre(v, t); err != nil {
		return nil, err
	}
	if t-v.curStart < m.cfg.MinWaitSeconds {
		return nil, newError(ErrConditionWait, "minimum wait duration not met")
	}
	if v.curCnt < m.cfg.MinContacts {
		return nil, newError(ErrConditionContact, "minimum contact count not met")
	}
	e := naEvent{kind: "judge", orderID: id}
	m.apply(v, &naEvent{kind: "judge", orderID: id, t: t})
	return m.accept(e, v, t), nil
}

// SubmitCorrection submits a corrected address within the correction window.
func (m *NaiveModel) SubmitCorrection(id string, t int, newAddr Address) (*Order, error) {
	_, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	// Stored awaiting-correction at/after its endpoint: window error first.
	if storedStatus := m.replay(id).status; storedStatus == OrderAwaitingCorrection &&
		v.curTyp == ExceptionWrongAddress && t >= v.windowEnd {
		m.mature(v, t)
		m.log = append(m.log, naEvent{kind: "observe", orderID: id, t: t})
		if t > m.maxT {
			m.maxT = t
		}
		return nil, newError(ErrCorrectionWindow, "correction window expired")
	}
	switch v.status {
	case OrderReturned, OrderReturnUnconfirmed, OrderDisposed:
		return nil, newError(ErrOrderTerminal, "order already terminal")
	case OrderReturning:
		return nil, newError(ErrActiveException, "order is being returned")
	case OrderAwaitingCorrection:
		switch {
		case v.curTyp != ExceptionWrongAddress:
			return nil, newError(ErrTypeMismatch, "open exception is not wrong address")
		case v.curPhase != ExceptionOpen:
			return nil, newError(ErrExceptionClosed, "exception already closed")
		case v.address.Distance(newAddr) > m.cfg.MaxCorrectionDist:
			return nil, newError(ErrDistanceExceeded, "corrected address too far from original")
		}
		e := naEvent{kind: "correct", orderID: id, addr: newAddr}
		m.apply(v, &naEvent{kind: "correct", orderID: id, t: t, addr: newAddr})
		return m.accept(e, v, t), nil
	default:
		return nil, newError(ErrExceptionNotFound, "no awaiting-correction exception")
	}
}

// RiderReturn marks that the rider brought the goods back.
func (m *NaiveModel) RiderReturn(id string, t int) (*Order, error) {
	stored, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	if stored.status != OrderReturning || stored.windowEnd != 0 {
		return nil, newError(ErrTypeMismatch, "rider return invalid in current status")
	}
	e := naEvent{kind: "return", orderID: id}
	m.apply(v, &naEvent{kind: "return", orderID: id, t: t})
	return m.accept(e, v, t), nil
}

// MerchantConfirm records merchant confirmation of returned goods.
func (m *NaiveModel) MerchantConfirm(id string, t int) (*Order, error) {
	_, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	stored := m.replay(id)
	switch {
	case stored.status != OrderReturning && stored.status != OrderReturnUnconfirmed:
		return nil, newError(ErrTypeMismatch, "order is not in returning")
	case stored.status == OrderReturnUnconfirmed:
		return nil, newError(ErrOrderTerminal, "return already unconfirmed")
	case stored.windowEnd == 0:
		return nil, newError(ErrTypeMismatch, "goods not handed back yet")
	case t >= stored.windowEnd:
		m.mature(v, t)
		m.log = append(m.log, naEvent{kind: "observe", orderID: id, t: t})
		if t > m.maxT {
			m.maxT = t
		}
		return nil, newError(ErrConfirmWindow, "merchant confirmation window expired")
	}
	switch v.status {
	case OrderReturning:
	default:
		return nil, newError(ErrTypeMismatch, "order is not in returning")
	}
	e := naEvent{kind: "confirm", orderID: id}
	m.apply(v, &naEvent{kind: "confirm", orderID: id, t: t})
	return m.accept(e, v, t), nil
}

// Get returns the order snapshot observed at time t.
func (m *NaiveModel) Get(id string, t int) (*Order, error) {
	_, v, err := m.begin(id, t)
	if err != nil {
		return nil, err
	}
	m.log = append(m.log, naEvent{kind: "observe", orderID: id, t: t})
	if t > m.maxT {
		m.maxT = t
	}
	return v.toOrder(), nil
}
