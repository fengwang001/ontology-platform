package pharmacy

// RegisterDrug 登记药品：整盒量（正整数）与是否可拆零。
func (e *Engine) RegisterDrug(now int, id string, boxSize int, splittable bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validTime(now) || !validID(id) || boxSize < 1 || boxSize > MaxQty {
		return newError(ErrInvalidParam, "药品登记参数非法: now=%d id=%q box=%d", now, id, boxSize)
	}
	return e.run(now, func() error {
		if _, ok := e.drugs[id]; ok {
			return newError(ErrStateMismatch, "药品 %q 已登记", id)
		}
		d := &Drug{id: id, box: boxSize, splittable: splittable}
		d.bq.init()
		if e.journaling {
			e.journal = append(e.journal, func() { delete(e.drugs, id) })
		}
		e.drugs[id] = d
		return nil
	})
}

// Inbound 入库（到货）：增加在库量，并在到货时刻按欠药登记次序分配。
func (e *Engine) Inbound(now int, drugID string, qty int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validTime(now) || !validID(drugID) || !validQty(qty) {
		return newError(ErrInvalidParam, "入库参数非法: now=%d drug=%q qty=%d", now, drugID, qty)
	}
	return e.run(now, func() error {
		d := e.drugs[drugID]
		if d == nil {
			return newError(ErrNotFound, "药品 %q 不存在", drugID)
		}
		set(e, &d.onHand, d.onHand+qty)
		set(e, &d.totalInbound, d.totalInbound+qty)
		e.allocate(d, now)
		return nil
	})
}

func validateRxInput(now int, in RxInput) error {
	if !validTime(now) || !validID(in.ID) || !validID(in.Patient) {
		return newError(ErrInvalidParam, "处方标识非法: id=%q patient=%q", in.ID, in.Patient)
	}
	if !validTime(in.IssueTime) || in.IssueTime > now {
		return newError(ErrInvalidParam, "开具时刻 %d 非法（须为 [0,%d] 内且不晚于 now=%d）", in.IssueTime, MaxTime, now)
	}
	if len(in.Lines) < 1 || len(in.Lines) > MaxLines {
		return newError(ErrInvalidParam, "处方行数 %d 超出 [1,%d]", len(in.Lines), MaxLines)
	}
	seen := make(map[string]bool, len(in.Lines))
	for _, l := range in.Lines {
		if !validID(l.DrugID) || !validQty(l.Qty) {
			return newError(ErrInvalidParam, "处方行非法: drug=%q qty=%d", l.DrugID, l.Qty)
		}
		if seen[l.DrugID] {
			return newError(ErrInvalidParam, "处方中药品 %q 重复", l.DrugID)
		}
		seen[l.DrugID] = true
	}
	return nil
}

// AcceptPrescription 受理处方：为每一行预留库存，缺口登记为欠药。
// 整单发放的处方任一行不能一次满足则整单拒绝（缺药），不留副作用。
func (e *Engine) AcceptPrescription(now int, in RxInput) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := validateRxInput(now, in); err != nil {
		return err
	}
	return e.run(now, func() error {
		for _, l := range in.Lines {
			if e.drugs[l.DrugID] == nil {
				return newError(ErrNotFound, "药品 %q 不存在", l.DrugID)
			}
		}
		if _, ok := e.rxs[in.ID]; ok {
			return newError(ErrStateMismatch, "处方 %q 已受理", in.ID)
		}
		if now > in.IssueTime+ValidityMinutes {
			return newError(ErrPrescriptionExpired, "处方 %q 于 %d 已过期", in.ID, in.IssueTime+ValidityMinutes+1)
		}
		if in.WholeOrder {
			for _, l := range in.Lines {
				d := e.drugs[l.DrugID]
				r := reserveAmount(d.box, d.splittable, l.Qty, d.onHand-d.reserved)
				if r < l.Qty {
					return newError(ErrOutOfStock, "药品 %q 可用量 %d 无法满足需求 %d", l.DrugID, d.onHand-d.reserved, l.Qty)
				}
			}
		}
		p := &Prescription{id: in.ID, patient: in.Patient, issue: in.IssueTime, whole: in.WholeOrder, status: RxActive}
		if e.journaling {
			e.journal = append(e.journal, func() { delete(e.rxs, in.ID) })
		}
		e.rxs[in.ID] = p
		for i, li := range in.Lines {
			d := e.drugs[li.DrugID]
			line := &Line{rx: p, drug: d, idx: i, demand: li.Qty}
			p.lines = append(p.lines, line)
			r := reserveAmount(d.box, d.splittable, li.Qty, d.onHand-d.reserved)
			if r > 0 {
				e.createReservation(line, r, now)
			}
			bo := li.Qty - r
			if bo < 0 {
				bo = 0 // 向上取整多出部分不算欠药
			}
			if bo > 0 {
				set(e, &line.backorder, bo)
				set(e, &d.backorderTotal, d.backorderTotal+bo)
				n := &boNode{line: line}
				e.boPush(d, n)
				set(e, &line.bo, n)
			}
		}
		e.pushEvent(in.IssueTime+ValidityMinutes+1, evRxExpiry, nil, p)
		return nil
	})
}

// Dispense 取药：取走处方全部当前有效预留，转为已发放。
func (e *Engine) Dispense(now int, rxID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validTime(now) || !validID(rxID) {
		return newError(ErrInvalidParam, "取药参数非法: now=%d rx=%q", now, rxID)
	}
	return e.run(now, func() error {
		p := e.rxs[rxID]
		if p == nil {
			return newError(ErrNotFound, "处方 %q 不存在", rxID)
		}
		total := 0
		for _, l := range p.lines {
			old := l.reservations
			if e.journaling {
				e.journal = append(e.journal, func() { l.reservations = old })
			}
			for _, res := range old {
				if !res.active {
					continue
				}
				set(e, &res.active, false)
				set(e, &l.reserved, l.reserved-res.qty)
				set(e, &l.dispensed, l.dispensed+res.qty)
				set(e, &l.drug.reserved, l.drug.reserved-res.qty)
				set(e, &l.drug.onHand, l.drug.onHand-res.qty)
				total += res.qty
			}
			l.reservations = nil
		}
		if total == 0 {
			return newError(ErrNoValidReservation, "处方 %q 无有效预留", rxID)
		}
		complete := true
		for _, l := range p.lines {
			if l.dispensed < l.demand || l.reserved > 0 {
				complete = false
				break
			}
		}
		if complete {
			set(e, &p.status, RxCompleted)
		}
		return nil
	})
}

// CancelPrescription 取消处方：效果与过期相同；仅适用于未完成且未过期的处方。
func (e *Engine) CancelPrescription(now int, rxID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validTime(now) || !validID(rxID) {
		return newError(ErrInvalidParam, "取消参数非法: now=%d rx=%q", now, rxID)
	}
	return e.run(now, func() error {
		p := e.rxs[rxID]
		if p == nil {
			return newError(ErrNotFound, "处方 %q 不存在", rxID)
		}
		if p.status != RxActive {
			return newError(ErrStateMismatch, "处方 %q 状态为 %s，不可取消", rxID, p.status)
		}
		e.terminate(p, RxCancelled, now)
		return nil
	})
}

func lineStatus(l *Line) LineStatus {
	if l.dispensed >= l.demand && l.reserved == 0 && l.backorder == 0 {
		return LineFulfilled
	}
	if l.rx.status == RxExpired || l.rx.status == RxCancelled {
		return LineVoid
	}
	if l.backorder > 0 {
		if l.reserved > 0 {
			return LinePartial
		}
		return LineBackordered
	}
	if l.reserved > 0 {
		return LineReserved
	}
	return LineBackordered
}

// QueryPrescription 查询处方各行状态。查询也是操作：推进事件并走钟。
func (e *Engine) QueryPrescription(now int, rxID string) (RxState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validTime(now) || !validID(rxID) {
		return RxState{}, newError(ErrInvalidParam, "查询参数非法: now=%d rx=%q", now, rxID)
	}
	var out RxState
	err := e.run(now, func() error {
		p := e.rxs[rxID]
		if p == nil {
			return newError(ErrNotFound, "处方 %q 不存在", rxID)
		}
		out = RxState{ID: p.id, Patient: p.patient, IssueTime: p.issue, WholeOrder: p.whole, Status: p.status}
		for _, l := range p.lines {
			out.Lines = append(out.Lines, LineState{
				DrugID:    l.drug.id,
				Demand:    l.demand,
				Reserved:  l.reserved,
				Dispensed: l.dispensed,
				Backorder: l.backorder,
				Status:    lineStatus(l),
			})
		}
		return nil
	})
	return out, err
}

// QueryDrug 查询药品在库量、有效预留量与欠药总量。欠药总量为 O(1) 计数。
func (e *Engine) QueryDrug(now int, drugID string) (DrugState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validTime(now) || !validID(drugID) {
		return DrugState{}, newError(ErrInvalidParam, "查询参数非法: now=%d drug=%q", now, drugID)
	}
	var out DrugState
	err := e.run(now, func() error {
		d := e.drugs[drugID]
		if d == nil {
			return newError(ErrNotFound, "药品 %q 不存在", drugID)
		}
		out = DrugState{
			ID:             d.id,
			BoxSize:        d.box,
			Splittable:     d.splittable,
			OnHand:         d.onHand,
			Reserved:       d.reserved,
			Available:      d.onHand - d.reserved,
			BackorderTotal: d.backorderTotal,
		}
		return nil
	})
	return out, err
}
