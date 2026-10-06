package registry

// RegisterFacility 登记设施：持有人、资格生效期（之后不可更改）。
// 重复登记或空持有人视为参数非法；资格生效期不可更改。
func (r *Registry) RegisterFacility(id, holder string, startPeriod int64) *Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == "" || holder == "" || !r.validPeriod(startPeriod) {
		r.logf("RegisterFacility(%q,%q,%d) -> 参数非法", id, holder, startPeriod)
		return errf(CodeInvalidParam, "设施/持有人为空或生效期越界")
	}
	if r.facs[id] != nil {
		r.logf("RegisterFacility(%q) -> 参数非法: 设施已存在，生效期不可更改", id)
		return errf(CodeInvalidParam, "设施已登记，资格生效期不可更改")
	}
	r.facs[id] = &facility{id: id, holder: holder, start: startPeriod, periods: map[int64]*facPeriod{}}
	r.logf("RegisterFacility(%q,holder=%q,start=%d) -> ok", id, holder, startPeriod)
	return nil
}

// SetQualificationEnd 登记或推后资格终止期（左闭右开）。
// 终止期一旦登记不可提前，只可推后或撤销；不得使已核发证书落到区间外。
func (r *Registry) SetQualificationEnd(id string, endPeriod int64) *Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.getFacility(id)
	if f == nil || !r.validPeriod(endPeriod) {
		r.logf("SetQualificationEnd(%q,%d) -> 参数非法", id, endPeriod)
		return errf(CodeInvalidParam, "设施不存在或终止期越界")
	}
	if endPeriod < f.start || endPeriod < f.end {
		r.logf("SetQualificationEnd(%q,%d) -> 参数非法: start=%d oldEnd=%d 不可提前", id, endPeriod, f.start, f.end)
		return errf(CodeInvalidParam, "终止期不得早于生效期或提前于既有终止期")
	}
	if f.maxIssued != 0 && endPeriod <= f.maxIssued {
		r.logf("SetQualificationEnd(%q,%d) -> 与已核发冲突 maxIssued=%d", id, endPeriod, f.maxIssued)
		return errf(CodeConflictIssued, "终止期不得早于已核发最大发电期的下一期")
	}
	f.end = endPeriod
	r.emit(Event{Kind: EventQualEndSet, Facility: id, EndPeriod: endPeriod})
	r.logf("SetQualificationEnd(%q,end=%d) -> ok", id, endPeriod)
	return nil
}

// RevokeQualificationEnd 撤销资格终止期，重新变为开放。
// 开放区间必然包含全部已核发期，撤销后可按同样约束重登。
func (r *Registry) RevokeQualificationEnd(id string) *Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.getFacility(id)
	if f == nil {
		r.logf("RevokeQualificationEnd(%q) -> 参数非法: 设施不存在", id)
		return errf(CodeInvalidParam, "设施不存在")
	}
	f.end = 0
	r.emit(Event{Kind: EventQualEndRevoked, Facility: id})
	r.logf("RevokeQualificationEnd(%q) -> ok", id)
	return nil
}

// RegisterMeter 登记/修正设施某发电期计量电量。
// 重复登记同一发电期视为修正。被拒绝的核发不占序号。
func (r *Registry) RegisterMeter(id string, genPeriod, qty int64) *Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.getFacility(id)
	if f == nil || !r.validPeriod(genPeriod) || qty < 0 {
		r.logf("RegisterMeter(%q,p=%d,q=%d) -> 参数非法", id, genPeriod, qty)
		return errf(CodeInvalidParam, "设施不存在、期编号越界或电量为负")
	}
	if !f.qualified(genPeriod) {
		r.logf("RegisterMeter(%q,p=%d) -> 资格外 start=%d end=%d", id, genPeriod, f.start, f.end)
		return errf(CodeOutsideQualification, "发电期不在资格区间内")
	}

	fp := f.periods[genPeriod]
	if fp == nil {
		fp = &facPeriod{held: newHeldHeap()}
		f.periods[genPeriod] = fp
		fp.inRem = f.rem
	}
	fp.qty = qty
	want := int((qty + fp.inRem) / r.cfg.UnitQty)

	r.adjustPeriod(f, genPeriod, fp, want)

	f.rem = fp.outRem
	if fp.issued > 0 && genPeriod > f.maxIssued {
		f.maxIssued = genPeriod
	}
	r.emit(Event{Kind: EventMeterRegistered, Facility: id, Period: genPeriod, Qty: qty, Remainder: fp.outRem})
	r.logf("RegisterMeter(%q,p=%d,q=%d,inRem=%d,want=%d,outRem=%d) -> ok",
		id, genPeriod, qty, fp.inRem, want, fp.outRem)
	return nil
}

// adjustPeriod 使该期非撤销张数变为 want：向下先撤销持有（序号降序），
// 再撤销已注销（注销时刻降序）；向上按差额核发新序号。
func (r *Registry) adjustPeriod(f *facility, genPeriod int64, fp *facPeriod, want int) {
	if fp.active > want {
		need := fp.active - want
		for need > 0 {
			s := fp.held.popMax()
			if s != 0 && r.certs[s].Status == StatusHeld {
				r.revokeHeld(f, genPeriod, fp, s)
				need--
				continue
			}
			// 无可撤销持证书：取最晚注销项（切片尾）。
			last := len(fp.cancelled) - 1
			rec := fp.cancelled[last]
			fp.cancelled = fp.cancelled[:last]
			r.revokeCancelled(fp, rec)
			need--
		}
	} else if want > fp.active {
		r.issueNew(f, genPeriod, fp, want-fp.active)
	}
	fp.outRem = fp.qty + fp.inRem - int64(fp.active)*r.cfg.UnitQty
}

func (r *Registry) issueNew(f *facility, genPeriod int64, fp *facPeriod, n int) {
	serials := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		r.serial++
		s := r.serial
		r.certs[s] = &Cert{Serial: s, Facility: f.id, Period: genPeriod, Holder: f.holder, Status: StatusHeld}
		fp.held.push(s)
		fp.active++
		fp.issued++
		serials = append(serials, s)
		r.emit(Event{Kind: EventIssued, Facility: f.id, Period: genPeriod, Serial: s, Holder: f.holder})
	}
}

func (r *Registry) revokeHeld(f *facility, genPeriod int64, fp *facPeriod, s int64) {
	c := r.certs[s]
	c.Status = StatusRevoked
	fp.active--
	r.emit(Event{Kind: EventRevokedHeld, Serial: s, Facility: f.id, Period: genPeriod})
}

func (r *Registry) revokeCancelled(fp *facPeriod, rec cancelRec) {
	c := r.certs[rec.Serial]
	consumer, usePeriod := c.Consumer, c.UsePeriod
	c.Status = StatusRevoked
	fp.active--
	cp := r.cons[consumer].periods[usePeriod]
	cp.cancelled--
	r.emit(Event{Kind: EventDeclarationInvalidated, Consumer: consumer, UsePeriod: usePeriod,
		Serial: rec.Serial, Facility: c.Facility, Period: c.Period})
}

// Remainder 返回设施当前账户余量（整数电量单位口径）。
// 单位由配置 UnitQty 决定时，返回的是“电量单位”口径的原始余量。
func (r *Registry) Remainder(id string) (int64, *Error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.getFacility(id)
	if f == nil {
		return 0, errf(CodeInvalidParam, "设施不存在")
	}
	return f.rem, nil
}
