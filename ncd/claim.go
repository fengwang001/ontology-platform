package ncd

// Claim 出险责任登记。
type Claim struct {
	ID          string // 事故编号，被保人内唯一
	AccidentDay int    // 事故日
	Ratio       int    // 责任比例 0..100，不小于有责门槛即为有责
	YearIndex   int    // 事故日归入的保单年度下标
}

// RegisterClaim 登记出险。事故日早于当前保单年度生效日时，归入事故日所在的
// 历史保单年度（迟报），并追溯重定级自该年度之后的每一次连续续保。
// 事故编号重复报事故已存在；事故日不在任何保单年度内报事故日未承保。
func (e *Engine) RegisterClaim(id, claimID string, accidentDay, ratio, day int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || claimID == "" || day < 0 || accidentDay < 0 || accidentDay > day ||
		ratio < 0 || ratio > 100 {
		return errf(ErrInvalidParam,
			"出险登记参数非法: id=%q claim=%q accidentDay=%d ratio=%d day=%d",
			id, claimID, accidentDay, ratio, day)
	}
	ins, err := e.insured(id)
	if err != nil {
		return err
	}
	if err := e.checkClock(day); err != nil {
		return err
	}
	if _, dup := ins.Claims[claimID]; dup {
		return errf(ErrClaimExists, "事故已存在: %s", claimID)
	}
	k := findYear(ins, accidentDay)
	if k < 0 {
		return errf(ErrAccidentNotCovered, "事故日 %d 不在被保人 %s 任何保单年度内", accidentDay, id)
	}
	e.clock = day
	c := &Claim{ID: claimID, AccidentDay: accidentDay, Ratio: ratio, YearIndex: k}
	ins.Claims[claimID] = c
	y := ins.Years[k]
	y.Claims = append(y.Claims, c)
	e.regrade(ins, k+1)
	return nil
}

// WithdrawClaim 撤销出险。撤销同样触发追溯重定级，
// 结果与该事故从未登记时逐年续保得到的等级完全一致。
func (e *Engine) WithdrawClaim(id, claimID string, day int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || claimID == "" || day < 0 {
		return errf(ErrInvalidParam, "出险撤销参数非法: id=%q claim=%q day=%d", id, claimID, day)
	}
	ins, err := e.insured(id)
	if err != nil {
		return err
	}
	if err := e.checkClock(day); err != nil {
		return err
	}
	c, ok := ins.Claims[claimID]
	if !ok {
		return errf(ErrClaimNotFound, "事故不存在: %s", claimID)
	}
	e.clock = day
	delete(ins.Claims, claimID)
	y := ins.Years[c.YearIndex]
	for i, oc := range y.Claims {
		if oc.ID == claimID {
			y.Claims = append(y.Claims[:i], y.Claims[i+1:]...)
			break
		}
	}
	e.regrade(ins, c.YearIndex+1)
	return nil
}
