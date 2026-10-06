package registry

// RegisterConsumption 用电方登记/修正某用电期用电量。
// 修正不得低于该期已注销总量，否则报低于已注销量。
func (r *Registry) RegisterConsumption(id string, usePeriod, qty int64) *Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == "" || !r.validPeriod(usePeriod) || qty < 0 {
		r.logf("RegisterConsumption(%q,p=%d,q=%d) -> 参数非法", id, usePeriod, qty)
		return errf(CodeInvalidParam, "用电方为空、期编号越界或电量为负")
	}
	c := r.getConsumer(id)
	cp := c.periods[usePeriod]
	if cp == nil {
		cp = &conPeriod{qty: -1}
		c.periods[usePeriod] = cp
	}
	if qty < cp.cancelled {
		r.logf("RegisterConsumption(%q,p=%d,q=%d) -> 低于已注销量 cancelled=%d",
			id, usePeriod, qty, cp.cancelled)
		return errf(CodeBelowCancelled, "修正电量低于已注销总量")
	}
	cp.qty = qty
	r.emit(Event{Kind: EventConsumptionRegistered, Consumer: id, UsePeriod: usePeriod, Qty: qty})
	r.logf("RegisterConsumption(%q,p=%d,q=%d,cancelled=%d) -> ok", id, usePeriod, qty, cp.cancelled)
	return nil
}

// Declare 用电方把持有的若干证书整批注销到某用电期。
func (r *Registry) Declare(consumer string, usePeriod int64, serials []int64) *Error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if e := r.declareParamCheck(consumer, usePeriod, serials); e != nil {
		r.logf("Declare(%q,p=%d,%v) -> %s", consumer, usePeriod, serials, e)
		return e
	}
	c := r.getConsumer(consumer)
	cp := c.periods[usePeriod]
	earliest := usePeriod - r.cfg.MaxAgePeriods
	ordered := sortedUnique(serials)
	for _, s := range ordered {
		cert := r.certs[s]
		if cert == nil {
			return r.declareFail(consumer, usePeriod, serials, errAt(CodeInvalidParam, s, "证书序号不存在"))
		}
		if cert.Status != StatusHeld {
			return r.declareFail(consumer, usePeriod, serials, errAt(CodeStateNotAllowed, s, "证书非持有状态"))
		}
		if cert.Holder != consumer {
			return r.declareFail(consumer, usePeriod, serials, errAt(CodeNotHolder, s, "证书非该用电方持有"))
		}
		if cert.Period > usePeriod || cert.Period < earliest {
			return r.declareFail(consumer, usePeriod, serials, errAt(CodePeriodMismatch, s, "发电期与用电期期限不符"))
		}
	}

	registered, cancelled := int64(0), int64(0)
	if cp != nil {
		registered, cancelled = cp.qty, cp.cancelled
	}
	add := int64(len(ordered)) * r.cfg.UnitQty
	if registered < 0 || cancelled+add > registered {
		r.logf("Declare(%q,p=%d,%v) -> 超出用电量 registered=%d cancelled=%d add=%d",
			consumer, usePeriod, serials, registered, cancelled, add)
		return errf(CodeOverConsumption, "注销总量将超过登记用电量")
	}

	for _, s := range ordered {
		r.cseq++
		cert := r.certs[s]
		cert.Status = StatusCancelled
		cert.Consumer = consumer
		cert.UsePeriod = usePeriod
		cert.CancelSeq = r.cseq
		fp := r.facs[cert.Facility].periods[cert.Period]
		fp.held.remove(s)
		fp.cancelled = append(fp.cancelled, cancelRec{Serial: s, CancelSeq: r.cseq})
		r.emit(Event{Kind: EventCancelled, Serial: s, Consumer: consumer, UsePeriod: usePeriod,
			Facility: cert.Facility, Period: cert.Period})
	}
	cp.cancelled += add
	r.logf("Declare(%q,p=%d,%v) -> ok cancelled=%d", consumer, usePeriod, ordered, cp.cancelled)
	return nil
}

func (r *Registry) declareFail(consumer string, usePeriod int64, serials []int64, e *Error) *Error {
	r.logf("Declare(%q,p=%d,%v) -> %s", consumer, usePeriod, serials, e)
	return e
}

func (r *Registry) declareParamCheck(consumer string, usePeriod int64, serials []int64) *Error {
	if consumer == "" || !r.validPeriod(usePeriod) {
		return errf(CodeInvalidParam, "用电方为空或用电期越界")
	}
	if len(serials) == 0 {
		return errf(CodeInvalidParam, "批数量非正")
	}
	seen := make(map[int64]struct{}, len(serials))
	for _, s := range serials {
		if s <= 0 {
			return errAt(CodeInvalidParam, s, "证书序号非正")
		}
		if _, ok := seen[s]; ok {
			return errAt(CodeInvalidParam, s, "批内序号重复")
		}
		seen[s] = struct{}{}
	}
	return nil
}

// CancelledQty 返回用电方某用电期的当前有效注销量。
func (r *Registry) CancelledQty(consumer string, usePeriod int64) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.cons[consumer]
	if c == nil {
		return 0
	}
	return c.periods[usePeriod].cancelledOrZero()
}

func (p *conPeriod) cancelledOrZero() int64 {
	if p == nil {
		return 0
	}
	return p.cancelled
}

// Events 返回事件日志副本。
func (r *Registry) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}
