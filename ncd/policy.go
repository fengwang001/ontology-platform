package ncd

const (
	policyYearDays = 365 // 保单年度天数，左端包含、右端不包含
	renewEarlyDays = 30  // 到期日前可办理续保的天数（含）
)

type claimRec struct {
	id    string
	day   int
	ratio int
}

type yearKind uint8

const (
	kindFirst       yearKind = iota // 首次投保
	kindRenewal                     // 连续续保
	kindInterrupted                 // 中断后重新投保（等级清零）
	kindTransfer                    // 跨车转移（继承等级与出险记录）
)

// yearRec 为一张保单承载的一个保单年度记录。
type yearRec struct {
	start     int
	end       int // 承保区间 [start, end)；转移终止时 end 收缩为终止日
	level     int
	claims    []claimRec
	protected bool
	kind      yearKind
	movedOut  bool // 转移源年度：出险记录已移交承继年度
}

type insured struct {
	years      []*yearRec
	claimIdx   map[string]int // 事故编号 -> 归入户度下标
	surcharges []Surcharge
}

func newInsured() *insured {
	return &insured{claimIdx: make(map[string]int)}
}

func (ins *insured) current() *yearRec {
	if len(ins.years) == 0 {
		return nil
	}
	return ins.years[len(ins.years)-1]
}

// grade 由上一保单年度裁定续保等级：
// 无有责出险升一级（封顶）；每次有责降两级（保底 0）；有责达三次直接归 0。
// 保护仅抵消第一次有责的降级：该年度确有有责出险，故不升；也不再降。
// 保护不影响「三次及以上归 0」的次数统计。
func grade(level, liable int, protected bool, maxLevel int) int {
	if liable >= 3 {
		return 0
	}
	effective := liable
	if protected && effective > 0 {
		effective--
	}
	if effective == 0 {
		if liable == 0 && level < maxLevel {
			return level + 1
		}
		return level
	}
	if level -= 2 * effective; level < 0 {
		return 0
	}
	return level
}

func liableCount(claims []claimRec, threshold int) int {
	n := 0
	for _, c := range claims {
		if c.ratio >= threshold {
			n++
		}
	}
	return n
}

// gradeFrom 依据上一年度记录裁定等级，并计入定级开销。
func (e *Engine) gradeFrom(prev *yearRec) int {
	e.gradeSteps++
	return grade(prev.level, liableCount(prev.claims, e.cfg.LiabilityThreshold), prev.protected, e.cfg.MaxLevel)
}

// regrade 从事故归入户度的下一年度起顺序重算等级，范围限于该年度之后。
// 等级降低时产生追补记录；等级升高不退费、不记负额。
func (e *Engine) regrade(ins *insured, from int) {
	for i := from; i < len(ins.years); i++ {
		y := ins.years[i]
		var lvl int
		switch y.kind {
		case kindRenewal:
			lvl = e.gradeFrom(ins.years[i-1])
		case kindTransfer:
			lvl = ins.years[i-1].level
		default:
			continue // 首保/中断年度等级恒为 0，不受此前年度影响
		}
		if lvl < y.level {
			ins.surcharges = append(ins.surcharges, Surcharge{
				YearStart: y.start,
				OldLevel:  y.level,
				NewLevel:  lvl,
				Amount:    e.cfg.Premiums[lvl] - e.cfg.Premiums[y.level],
			})
		}
		y.level = lvl
	}
}

// IssuePolicy 为被保人办理首次投保，等级为 0，年度自办理日起算。
func (e *Engine) IssuePolicy(id string, day int) error {
	const op = "IssuePolicy"
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || day < 0 {
		return newErr(op, ErrInvalidParam, "被保人编号为空或日为负")
	}
	ins, err := e.mustInsured(op, id)
	if err != nil {
		return err
	}
	if err := e.checkClock(op, day); err != nil {
		return err
	}
	if len(ins.years) > 0 {
		return newErr(op, ErrActivePolicyExists, "被保人 %q 已有承载等级的保单", id)
	}
	ins.years = append(ins.years, &yearRec{
		start: day, end: day + policyYearDays, kind: kindFirst,
	})
	e.commitClock(day)
	return nil
}

// Renew 办理续保。窗口 [到期-30, 到期+N] 内为连续续保，新年度自原到期日
// 起算；早于窗口报「续保窗口外」；晚于窗口视为中断，等级清零并按首次投保
// 处理（新年度自办理日起算）。
func (e *Engine) Renew(id string, day int) error {
	const op = "Renew"
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || day < 0 {
		return newErr(op, ErrInvalidParam, "被保人编号为空或日为负")
	}
	ins, err := e.mustInsured(op, id)
	if err != nil {
		return err
	}
	if err := e.checkClock(op, day); err != nil {
		return err
	}
	cur := ins.current()
	if cur == nil {
		return newErr(op, ErrInvalidParam, "被保人 %q 尚无可续保保单", id)
	}
	expiry := cur.start + policyYearDays
	switch {
	case day < expiry-renewEarlyDays:
		return newErr(op, ErrOutOfRenewalWindow, "续保窗口 [%d, %d]，办理日 %d", expiry-renewEarlyDays, expiry+e.cfg.RenewalLateDays, day)
	case day <= expiry+e.cfg.RenewalLateDays:
		ins.years = append(ins.years, &yearRec{
			start: expiry, end: expiry + policyYearDays,
			level: e.gradeFrom(cur), kind: kindRenewal,
		})
	default:
		ins.years = append(ins.years, &yearRec{
			start: day, end: day + policyYearDays, kind: kindInterrupted,
		})
	}
	e.commitClock(day)
	return nil
}

// Transfer 办理跨车转移：原保单于办理日终止，新保单继承等级与当前年度
// 的出险记录（保护状态不继承），新年度自办理日起算。
func (e *Engine) Transfer(id string, day int) error {
	const op = "Transfer"
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || day < 0 {
		return newErr(op, ErrInvalidParam, "被保人编号为空或日为负")
	}
	ins, err := e.mustInsured(op, id)
	if err != nil {
		return err
	}
	if err := e.checkClock(op, day); err != nil {
		return err
	}
	cur := ins.current()
	if cur == nil {
		return newErr(op, ErrInvalidParam, "被保人 %q 尚无可转移保单", id)
	}
	cur.end = day
	if cur.end < cur.start {
		cur.end = cur.start // 提前续保产生的未来年度尚未生效即转移
	}
	cur.movedOut = true
	next := &yearRec{
		start: day, end: day + policyYearDays,
		level: cur.level, claims: cur.claims, kind: kindTransfer,
	}
	cur.claims = nil
	idx := len(ins.years)
	for _, c := range next.claims {
		ins.claimIdx[c.id] = idx
	}
	ins.years = append(ins.years, next)
	e.commitClock(day)
	return nil
}

// BuyProtection 为当前保单年度购买一次等级保护。
func (e *Engine) BuyProtection(id string, day int) error {
	const op = "BuyProtection"
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || day < 0 {
		return newErr(op, ErrInvalidParam, "被保人编号为空或日为负")
	}
	ins, err := e.mustInsured(op, id)
	if err != nil {
		return err
	}
	if err := e.checkClock(op, day); err != nil {
		return err
	}
	cur := ins.current()
	if cur == nil {
		return newErr(op, ErrInvalidParam, "被保人 %q 尚无保单", id)
	}
	if cur.level < e.cfg.ProtectMinLevel {
		return newErr(op, ErrLevelTooLow, "等级 %d 低于保护起始级 %d", cur.level, e.cfg.ProtectMinLevel)
	}
	if cur.protected {
		return newErr(op, ErrProtectionBought, "当前保单年度已购买保护")
	}
	cur.protected = true
	e.commitClock(day)
	return nil
}
