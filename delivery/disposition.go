package delivery

// enterUndeliverable moves an open exception to undeliverable and runs the
// merchant's preset disposition at time t, paying compensation at most once.
func (p *Platform) enterUndeliverable(o *orderState, ex *exception, t int, liable Liability) {
	ex.phase = ExceptionUndeliverable
	o.liability = liable
	p.payCompensation(o)
	switch o.disposition {
	case DispositionReturn:
		o.status = OrderReturning
		o.windowEnd = 0
	default:
		o.status = OrderDisposed
		o.current = nil
	}
}

// applyTimeEffects converts every window of order o that has matured by t.
// Only the single order being acted upon is inspected, and only its own
// window endpoints are read: cost is O(1) in platform exception count and
// O(1) in this order's contact-history length.
func (p *Platform) applyTimeEffects(o *orderState, t int) {
	for {
		switch o.status {
		case OrderAwaitingCorrection:
			if t < o.windowEnd {
				return
			}
			ex := o.current
			// t >= windowEnd means the right endpoint is already excluded
			// and no valid correction was ever formed.
			enterWrongAddressUndeliverable(p, o, ex, t)
		case OrderReturning:
			if o.windowEnd == 0 || t < o.windowEnd {
				return
			}
			// Merchant missed the confirmation window: the merchant
			// bears the loss (overriding the original customer liability).
			o.liability = LiabilityMerchant
			o.status = OrderReturnUnconfirmed
			o.current = nil
			o.windowEnd = 0
		default:
			return
		}
	}
}

// payCompensation grants the rider compensation at most once per order.
func (p *Platform) payCompensation(o *orderState) {
	if o.compPaid {
		return
	}
	o.compPaid = true
}

func enterWrongAddressUndeliverable(p *Platform, o *orderState, ex *exception, t int) {
	// Wrong address whose customer never formed a valid correction:
	// liability falls on the customer.
	p.enterUndeliverable(o, ex, t, LiabilityCustomer)
}

// effectiveStatus is a pure projection: the status the order would have
// if every window due no later than t were converted. It mutates nothing,
// so rejected operations can classify errors against the matured view
// while leaving stored state untouched.
func effectiveStatus(o *orderState, t int) OrderStatus {
	status := o.status
	switch status {
	case OrderAwaitingCorrection:
		if t >= o.windowEnd {
			if o.disposition == DispositionReturn {
				return OrderReturning
			}
			return OrderDisposed
		}
	case OrderReturning:
		if o.windowEnd != 0 && t >= o.windowEnd {
			return OrderReturnUnconfirmed
		}
	}
	return status
}

// hasOpenException reports whether an in-progress exception exists under
// the matured view at time t.
func hasOpenException(o *orderState, t int) bool {
	ex := o.current
	if ex == nil || ex.phase != ExceptionOpen {
		return false
	}
	// A matured correction exception is no longer "in progress".
	if o.status == OrderAwaitingCorrection && t >= o.windowEnd {
		return false
	}
	return true
}
