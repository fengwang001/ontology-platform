package ncd

// ReportClaim 登记一次出险。事故日早于当前保单年度生效日时，归入事故日
// 所在的历史保单年度，并自该年度之后重定级每一次连续续保。
func (e *Engine) ReportClaim(id, claimID string, accidentDay, ratio, day int) error {
	const op = "ReportClaim"
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || claimID == "" || day < 0 || accidentDay < 0 || accidentDay > day || ratio < 0 || ratio > 100 {
		return newErr(op, ErrInvalidParam, "编号为空、日为负、事故日 %d 晚于办理日 %d 或责任比例 %d 越界", accidentDay, day, ratio)
	}
	ins, err := e.mustInsured(op, id)
	if err != nil {
		return err
	}
	if err := e.checkClock(op, day); err != nil {
		return err
	}
	if _, dup := ins.claimIdx[claimID]; dup {
		return newErr(op, ErrClaimExists, "事故编号重复: %q", claimID)
	}
	idx := ins.attrIndex(accidentDay)
	if idx < 0 {
		return newErr(op, ErrAccidentNotCovered, "事故日 %d 不在任何保单年度内", accidentDay)
	}
	ins.years[idx].claims = append(ins.years[idx].claims, claimRec{id: claimID, day: accidentDay, ratio: ratio})
	ins.claimIdx[claimID] = idx
	e.regrade(ins, idx+1)
	e.commitClock(day)
	return nil
}

// CancelClaim 撤销一次出险，并触发重定级；结果与该事故从未登记时一致。
func (e *Engine) CancelClaim(id, claimID string, day int) error {
	const op = "CancelClaim"
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || claimID == "" || day < 0 {
		return newErr(op, ErrInvalidParam, "编号为空或日为负")
	}
	ins, err := e.mustInsured(op, id)
	if err != nil {
		return err
	}
	if err := e.checkClock(op, day); err != nil {
		return err
	}
	idx, ok := ins.claimIdx[claimID]
	if !ok {
		return newErr(op, ErrClaimNotFound, "事故编号不存在: %q", claimID)
	}
	y := ins.years[idx]
	for i, c := range y.claims {
		if c.id == claimID {
			y.claims = append(y.claims[:i], y.claims[i+1:]...)
			break
		}
	}
	delete(ins.claimIdx, claimID)
	e.regrade(ins, idx+1)
	e.commitClock(day)
	return nil
}

// attrIndex 返回事故日所属的保单年度下标；转移源年度的迟报重定向到
// 承继年度（出险记录已随转移移交）。未承保返回 -1。
func (ins *insured) attrIndex(day int) int {
	for i := len(ins.years) - 1; i >= 0; i-- {
		y := ins.years[i]
		if day >= y.start && day < y.end {
			for y.movedOut && i+1 < len(ins.years) {
				i++
				y = ins.years[i]
			}
			return i
		}
	}
	return -1
}
