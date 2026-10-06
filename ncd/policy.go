package ncd

// Year 保单年度记录：[Start, End) 左闭右开，长度恒为 YearDays。
type Year struct {
	Start     int      // 生效日
	End       int      // 到期日（不含）
	Grade     int      // 本年度有效等级（续保时由上一年度裁定）
	ByRenewal bool     // 是否连续续保产生的年度；false 表示新保/中断后首保
	Protected bool     // 本年度是否已购买等级保护
	Claims    []*Claim // 归入本年度的出险（含迟报）
}

// Policy 保单：一张保单承载一个年度的等级；跨车转移终止旧保单、
// 在同一保单年度上签发新保单，从而继承等级与本年度出险记录。
type Policy struct {
	Vehicle   string // 车辆标识
	YearIndex int    // 承载的保单年度下标
	Active    bool   // 是否未终止（转移后置 false）
}

// YearInfo 保单年度只读视图。
type YearInfo struct {
	Start      int
	End        int
	Grade      int
	ByRenewal  bool
	Protected  bool
	ClaimCount int
}

// PolicyInfo 保单只读视图。
type PolicyInfo struct {
	Vehicle   string
	YearIndex int
	Active    bool
}

// OpenPolicy 办理新保：首次投保或中断后重新投保，等级为 0。
// 办理日已有在保保单时报已有在保保单，须先办理转移或待原保单到期。
func (e *Engine) OpenPolicy(id, vehicle string, day int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || vehicle == "" || day < 0 {
		return errf(ErrInvalidParam, "新保参数非法: id=%q vehicle=%q day=%d", id, vehicle, day)
	}
	ins, err := e.insured(id)
	if err != nil {
		return err
	}
	if err := e.checkClock(day); err != nil {
		return err
	}
	if p := activePolicyAt(ins, day); p != nil {
		return errf(ErrActivePolicyExists, "被保人 %s 已有在保保单(车辆 %s)", id, p.Vehicle)
	}
	e.clock = day
	ins.Years = append(ins.Years, &Year{Start: day, End: day + YearDays, Grade: 0})
	ins.Policies = append(ins.Policies, &Policy{Vehicle: vehicle, YearIndex: len(ins.Years) - 1, Active: true})
	return nil
}

// Renew 办理续保。办理日须落在 [到期日-30, 到期日+N] 窗口内，否则报续保窗口外
// （中断后应改用 OpenPolicy，等级清零按首次投保处理）。
// 新保单年度自原到期日起算，不因提前办理而提前生效。返回新年度等级。
func (e *Engine) Renew(id string, day int) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || day < 0 {
		return -1, errf(ErrInvalidParam, "续保参数非法: id=%q day=%d", id, day)
	}
	ins, err := e.insured(id)
	if err != nil {
		return -1, err
	}
	if err := e.checkClock(day); err != nil {
		return -1, err
	}
	n := len(ins.Years)
	if n == 0 {
		return -1, errf(ErrNoActivePolicy, "被保人 %s 从未投保，无法续保", id)
	}
	last := ins.Years[n-1]
	if day < last.End-RenewalEarlyDays || day > last.End+e.cfg.RenewalLateDays {
		return -1, errf(ErrOutOfRenewalWindow, "办理日 %d 不在续保窗口 [%d, %d]",
			day, last.End-RenewalEarlyDays, last.End+e.cfg.RenewalLateDays)
	}
	e.clock = day
	grade := e.adjudicate(last.Grade, last)
	ins.Years = append(ins.Years, &Year{
		Start: last.End, End: last.End + YearDays, Grade: grade, ByRenewal: true,
	})
	vehicle := ""
	if m := len(ins.Policies); m > 0 {
		vehicle = ins.Policies[m-1].Vehicle
	}
	ins.Policies = append(ins.Policies, &Policy{Vehicle: vehicle, YearIndex: n, Active: true})
	return grade, nil
}

// Transfer 跨车转移：终止当前在保保单，在同一保单年度上为新车签发保单，
// 新保单继承等级与本年度出险记录。当前无在保保单时报无在保保单。
func (e *Engine) Transfer(id, newVehicle string, day int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || newVehicle == "" || day < 0 {
		return errf(ErrInvalidParam, "转移参数非法: id=%q vehicle=%q day=%d", id, newVehicle, day)
	}
	ins, err := e.insured(id)
	if err != nil {
		return err
	}
	if err := e.checkClock(day); err != nil {
		return err
	}
	p := activePolicyAt(ins, day)
	if p == nil {
		return errf(ErrNoActivePolicy, "被保人 %s 当前无在保保单，无法转移", id)
	}
	e.clock = day
	p.Active = false
	ins.Policies = append(ins.Policies, &Policy{Vehicle: newVehicle, YearIndex: p.YearIndex, Active: true})
	return nil
}

// BuyProtection 购买等级保护：等级不低于保护起始级的被保人，
// 每个保单年度可购买一次。等级不足报等级不足，同一年度重复购买报已购买。
func (e *Engine) BuyProtection(id string, day int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || day < 0 {
		return errf(ErrInvalidParam, "保护购买参数非法: id=%q day=%d", id, day)
	}
	ins, err := e.insured(id)
	if err != nil {
		return err
	}
	if err := e.checkClock(day); err != nil {
		return err
	}
	p := activePolicyAt(ins, day)
	if p == nil {
		return errf(ErrNoActivePolicy, "被保人 %s 当前无在保保单，无法购买保护", id)
	}
	y := ins.Years[p.YearIndex]
	if y.Grade < e.cfg.ProtectStartGrade {
		return errf(ErrGradeTooLow, "等级 %d 低于保护起始级 %d", y.Grade, e.cfg.ProtectStartGrade)
	}
	if y.Protected {
		return errf(ErrProtectionBought, "被保人 %s 本年度已购买保护", id)
	}
	e.clock = day
	y.Protected = true
	// 若本年度已提前办理续保，保护会影响下一年度等级，需追溯重定级。
	e.regrade(ins, p.YearIndex+1)
	return nil
}

// Grade 返回被保人最新保单年度等级。
func (e *Engine) Grade(id string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ins, err := e.insured(id)
	if err != nil {
		return -1, err
	}
	if len(ins.Years) == 0 {
		return -1, errf(ErrNoActivePolicy, "被保人 %s 从未投保", id)
	}
	return ins.Years[len(ins.Years)-1].Grade, nil
}

// Years 返回被保人全部保单年度的只读视图。
func (e *Engine) Years(id string) ([]YearInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ins, err := e.insured(id)
	if err != nil {
		return nil, err
	}
	out := make([]YearInfo, len(ins.Years))
	for i, y := range ins.Years {
		out[i] = YearInfo{
			Start: y.Start, End: y.End, Grade: y.Grade,
			ByRenewal: y.ByRenewal, Protected: y.Protected, ClaimCount: len(y.Claims),
		}
	}
	return out, nil
}

// Policies 返回被保人全部保单的只读视图。
func (e *Engine) Policies(id string) ([]PolicyInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ins, err := e.insured(id)
	if err != nil {
		return nil, err
	}
	out := make([]PolicyInfo, len(ins.Policies))
	for i, p := range ins.Policies {
		out[i] = PolicyInfo{Vehicle: p.Vehicle, YearIndex: p.YearIndex, Active: p.Active}
	}
	return out, nil
}

// Catchups 返回被保人的保费差额追补记录。
func (e *Engine) Catchups(id string) ([]Catchup, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ins, err := e.insured(id)
	if err != nil {
		return nil, err
	}
	out := make([]Catchup, len(ins.Catchups))
	copy(out, ins.Catchups)
	return out, nil
}
