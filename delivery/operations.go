package delivery

// begin serializes an operation, validates the clock and resolves the
// order. It mutates nothing: rejected operations leave stored state,
// timing records and the clock untouched. Maturation due at t is applied
// only when pump is requested by an accepting operation or query.
func (p *Platform) begin(id string, t int, pump bool) (bool, *orderState, error) {
	p.mu.Lock()
	if err := p.clk.check(t); err != nil {
		p.mu.Unlock()
		return false, nil, err
	}
	o := p.orders[id]
	if o == nil {
		p.mu.Unlock()
		return false, nil, newError(ErrOrderNotFound, "order not found: "+id)
	}
	if pump {
		p.applyTimeEffects(o, t)
	}
	return true, o, nil
}

// fail releases the lock and returns a domain error.
func fail(p *Platform, code ErrorCode, msg string) error {
	p.mu.Unlock()
	return newError(code, msg)
}

func (p *Platform) commit(o *orderState, t int) *Order {
	p.clk.advance(t)
	s := o.snapshot()
	p.mu.Unlock()
	return s
}

// CreateOrder places an order at time t with a fixed merchant disposition.
func (p *Platform) CreateOrder(id string, t int, addr Address, disp Disposition) (*Order, error) {
	if id == "" || (disp != DispositionReturn && disp != DispositionOnSite) {
		return nil, newError(ErrInvalidParam, "invalid order id or disposition")
	}
	p.mu.Lock()
	if err := p.clk.check(t); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if _, exists := p.orders[id]; exists {
		p.mu.Unlock()
		return nil, newError(ErrInvalidParam, "duplicate order id: "+id)
	}
	o := &orderState{id: id, status: OrderCreated, address: addr, disposition: disp}
	p.orders[id] = o
	return p.commit(o, t), nil
}

// Pickup marks an existing order as picked up at time t.
func (p *Platform) Pickup(id string, t int) (*Order, error) {
	ok, o, err := p.begin(id, t, false)
	if !ok {
		return nil, err
	}
	if o.status != OrderCreated {
		return nil, fail(p, ErrInvalidParam, "pickup requires created order")
	}
	p.applyTimeEffects(o, t)
	o.status = OrderDelivering
	return p.commit(o, t), nil
}

// Deliver marks an order successfully delivered at time t.
func (p *Platform) Deliver(id string, t int) (*Order, error) {
	ok, o, err := p.begin(id, t, false)
	if !ok {
		return nil, err
	}
	switch effectiveStatus(o, t) {
	case OrderDelivering:
		if hasOpenException(o, t) {
			return nil, fail(p, ErrActiveException, "an exception is in progress")
		}
		p.applyTimeEffects(o, t)
		o.status = OrderDelivered
		return p.commit(o, t), nil
	case OrderCreated:
		return nil, fail(p, ErrNotPickedUp, "order not picked up")
	case OrderDelivered:
		return nil, fail(p, ErrAlreadyDelivered, "order already delivered")
	default:
		return nil, fail(p, statusCode(effectiveStatus(o, t)), "deliver invalid in current status")
	}
}

func statusCode(s OrderStatus) ErrorCode {
	switch s {
	case OrderAwaitingCorrection, OrderReturning:
		return ErrActiveException
	case OrderReturned, OrderReturnUnconfirmed, OrderDisposed:
		return ErrOrderTerminal
	default:
		return ErrTypeMismatch
	}
}

// beginReport validates shared report preconditions against the matured
// view. The caller allocates the new exception only after validation, so
// a rejected report changes nothing.
func (p *Platform) beginReport(id string, t int) (bool, *orderState, error) {
	ok, o, err := p.begin(id, t, false)
	if !ok {
		return false, nil, err
	}
	switch effectiveStatus(o, t) {
	case OrderCreated:
		return false, nil, fail(p, ErrNotPickedUp, "cannot report before pickup")
	case OrderDelivered:
		return false, nil, fail(p, ErrAlreadyDelivered, "cannot report after delivery")
	case OrderReturned, OrderReturnUnconfirmed, OrderDisposed:
		return false, nil, fail(p, ErrOrderTerminal, "order already terminal")
	case OrderAwaitingCorrection, OrderReturning:
		return false, nil, fail(p, ErrActiveException, "an exception is already in progress")
	case OrderDelivering:
		if hasOpenException(o, t) {
			return false, nil, fail(p, ErrActiveException, "an exception is already in progress")
		}
	}
	return true, o, nil
}

// ReportUnreachable opens an unreachable exception at time t.
func (p *Platform) ReportUnreachable(id string, t int) (*Exception, error) {
	ok, o, err := p.beginReport(id, t)
	if !ok {
		return nil, err
	}
	p.applyTimeEffects(o, t)
	o.seq++
	ex := &exception{id: o.seq, typ: ExceptionUnreachable, phase: ExceptionOpen, start: t}
	o.current = ex
	p.clk.advance(t)
	s := exceptionSnapshot(ex)
	p.mu.Unlock()
	return s, nil
}

// ReportWrongAddress opens a wrong-address exception at time t.
func (p *Platform) ReportWrongAddress(id string, t int) (*Exception, error) {
	ok, o, err := p.beginReport(id, t)
	if !ok {
		return nil, err
	}
	p.applyTimeEffects(o, t)
	o.seq++
	ex := &exception{id: o.seq, typ: ExceptionWrongAddress, phase: ExceptionOpen, start: t}
	ex.windowEnd = t + p.cfg.CorrectionWindow
	o.current = ex
	o.status = OrderAwaitingCorrection
	o.windowEnd = ex.windowEnd
	p.clk.advance(t)
	s := exceptionSnapshot(ex)
	p.mu.Unlock()
	return s, nil
}

// ReportRefusal opens a refusal exception with evidence; if accepted the
// order is undeliverable immediately.
func (p *Platform) ReportRefusal(id string, t int, evidenceID string, evidenceTime int) (*Exception, error) {
	if evidenceID == "" {
		return nil, newError(ErrInvalidParam, "evidence id required")
	}
	ok, o, err := p.beginReport(id, t)
	if !ok {
		return nil, err
	}
	if evidenceTime < t-p.cfg.EvidenceTTLSeconds {
		return nil, fail(p, ErrEvidenceInvalid, "refusal evidence out of validity window")
	}
	p.applyTimeEffects(o, t)
	o.seq++
	ex := &exception{id: o.seq, typ: ExceptionRefusal, phase: ExceptionOpen, start: t}
	ex.evidenceID = evidenceID
	ex.evidenceTime = evidenceTime
	o.current = ex
	p.enterUndeliverable(o, ex, t, LiabilityCustomer)
	p.clk.advance(t)
	snap := exceptionSnapshot(ex)
	p.mu.Unlock()
	return snap, nil
}

// openContactException resolves an unreachable-only action against the
// matured view.
func (p *Platform) openContactException(id string, t int) (bool, *orderState, *exception, error) {
	ok, o, err := p.begin(id, t, false)
	if !ok {
		return false, nil, nil, err
	}
	ex := o.current
	view := effectiveStatus(o, t)
	switch {
	case view == OrderReturning || view == OrderAwaitingCorrection:
		return false, nil, nil, fail(p, ErrActiveException, "an exception is already in progress")
	case view == OrderReturned || view == OrderReturnUnconfirmed || view == OrderDisposed:
		return false, nil, nil, fail(p, ErrOrderTerminal, "order already terminal")
	case ex == nil || !hasOpenException(o, t):
		if ex != nil && ex.phase != ExceptionOpen {
			return false, nil, nil, fail(p, ErrExceptionClosed, "exception already closed")
		}
		return false, nil, nil, fail(p, ErrExceptionNotFound, "no in-progress exception")
	case ex.typ != ExceptionUnreachable:
		return false, nil, nil, fail(p, ErrTypeMismatch, "open exception is not unreachable")
	}
	return true, o, ex, nil
}

// RecordContact logs one contact attempt at time t.
func (p *Platform) RecordContact(id string, t int) (*Exception, error) {
	ok, _, ex, err := p.openContactException(id, t)
	if !ok {
		return nil, err
	}
	if ex.cnt > 0 && t-ex.lastContactT < p.cfg.MinContactInterval {
		return nil, fail(p, ErrContactTooFrequent, "contact attempts too frequent")
	}
	ex.cnt++
	ex.lastContactT = t
	p.clk.advance(t)
	s := exceptionSnapshot(ex)
	p.mu.Unlock()
	return s, nil
}

// CustomerRespond closes an open unreachable exception at time t.
func (p *Platform) CustomerRespond(id string, t int) (*Order, error) {
	ok, o, ex, err := p.openContactException(id, t)
	if !ok {
		return nil, err
	}
	ex.phase = ExceptionClosed
	o.status = OrderDelivering
	return p.commit(o, t), nil
}

// JudgeUndeliverable attempts to pronounce an unreachable order
// undeliverable at time t.
func (p *Platform) JudgeUndeliverable(id string, t int) (*Order, error) {
	ok, o, ex, err := p.openContactException(id, t)
	if !ok {
		return nil, err
	}
	if t-ex.start < p.cfg.MinWaitSeconds {
		return nil, fail(p, ErrConditionWait, "minimum wait duration not met")
	}
	if ex.cnt < p.cfg.MinContacts {
		return nil, fail(p, ErrConditionContact, "minimum contact count not met")
	}
	p.enterUndeliverable(o, ex, t, LiabilityCustomer)
	return p.commit(o, t), nil
}

// SubmitCorrection submits a corrected address within the correction window.
func (p *Platform) SubmitCorrection(id string, t int, newAddr Address) (*Order, error) {
	ok, o, err := p.begin(id, t, false)
	if !ok {
		return nil, err
	}
	// Endpoint window check first: a stored awaiting-correction exception
	// observed at/after its right endpoint reports the window error even
	// though the matured view already converts the order.
	if o.status == OrderAwaitingCorrection && o.current != nil &&
		o.current.typ == ExceptionWrongAddress && t >= o.windowEnd {
		p.applyTimeEffects(o, t)
		p.clk.advance(t)
		return nil, fail(p, ErrCorrectionWindow, "correction window expired")
	}
	switch effectiveStatus(o, t) {
	case OrderReturned, OrderReturnUnconfirmed, OrderDisposed:
		return nil, fail(p, ErrOrderTerminal, "order already terminal")
	case OrderReturning:
		return nil, fail(p, ErrActiveException, "order is being returned")
	case OrderAwaitingCorrection:
		// Status class errors precede window errors; window precedes distance.
		ex := o.current
		switch {
		case ex == nil || ex.typ != ExceptionWrongAddress:
			return nil, fail(p, ErrTypeMismatch, "open exception is not wrong address")
		case ex.phase != ExceptionOpen:
			return nil, fail(p, ErrExceptionClosed, "exception already closed")
		case o.address.Distance(newAddr) > p.cfg.MaxCorrectionDist:
			return nil, fail(p, ErrDistanceExceeded, "corrected address too far from original")
		}
		o.address = newAddr
		o.addrChanges++
		ex.phase = ExceptionClosed
		o.status = OrderDelivering
		o.windowEnd = 0
		return p.commit(o, t), nil
	default:
		return nil, fail(p, ErrExceptionNotFound, "no awaiting-correction exception")
	}
}

// RiderReturn marks that the rider brought the goods back at time t.
func (p *Platform) RiderReturn(id string, t int) (*Order, error) {
	ok, o, err := p.begin(id, t, false)
	if !ok {
		return nil, err
	}
	switch {
	case o.status != OrderReturning:
		return nil, fail(p, ErrTypeMismatch, "order is not in returning")
	case o.windowEnd != 0:
		return nil, fail(p, ErrTypeMismatch, "return already registered")
	}
	{
		o.windowEnd = t + p.cfg.MerchantConfirmWindow
		return p.commit(o, t), nil
	}
}

// MerchantConfirm records merchant confirmation of receiving returned goods.
func (p *Platform) MerchantConfirm(id string, t int) (*Order, error) {
	ok, o, err := p.begin(id, t, false)
	if !ok {
		return nil, err
	}
	// Stored status distinguishes "not handed back" from a matured window.
	switch {
	case o.status != OrderReturning && o.status != OrderReturnUnconfirmed:
		return nil, fail(p, ErrTypeMismatch, "order is not in returning")
	case o.status == OrderReturnUnconfirmed:
		return nil, fail(p, ErrOrderTerminal, "return already unconfirmed")
	case o.windowEnd == 0:
		return nil, fail(p, ErrTypeMismatch, "goods not handed back yet")
	case t >= o.windowEnd:
		p.applyTimeEffects(o, t)
		p.clk.advance(t)
		return nil, fail(p, ErrConfirmWindow, "merchant confirmation window expired")
	}
	if effectiveStatus(o, t) != OrderReturning {
		return nil, fail(p, ErrTypeMismatch, "order is not in returning")
	}
	{
		o.status = OrderReturned
		o.windowEnd = 0
		o.current = nil
		return p.commit(o, t), nil
	}
}

// Get returns the order snapshot observed at time t, applying every time
// effect due no later than t.
func (p *Platform) Get(id string, t int) (*Order, error) {
	ok, o, err := p.begin(id, t, true)
	if !ok {
		return nil, err
	}
	return p.commit(o, t), nil
}
