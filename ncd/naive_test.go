package ncd

// 独立朴素模型：按规则逐年重放的第二实现，用于随机序列对照。
// 与引擎的差异：不维护任何增量汇总，有责次数每次从出险列表现数；
// 任何变更后从第一个年度起全量重放所有等级；年度定位用线性扫描。

type naiveYear struct {
	start     int
	base      bool
	protected bool
	level     int
	claims    []claimRec
}

type naiveInsured struct {
	years      []naiveYear
	surcharges []Surcharge
}

type naiveEngine struct {
	cfg Config
	now int
	ins map[string]*naiveInsured
}

func newNaive(cfg Config) *naiveEngine {
	return &naiveEngine{cfg: cfg, ins: make(map[string]*naiveInsured)}
}

// naiveGrade 与引擎 gradeNext 结构不同的独立裁定实现。
func naiveGrade(level, liable int, protected bool, maxLevel int) int {
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
	for ; effective > 0; effective-- {
		level -= 2
		if level <= 0 {
			return 0
		}
	}
	return level
}

func (n *naiveEngine) findYear(in *naiveInsured, day int) int {
	for i, y := range in.years {
		if day >= y.start && day < y.start+policyYearDays {
			return i
		}
	}
	return -1
}

// replayAll 全量重放：每年等级由前一年（重放后的）等级与出险现数裁定。
func (n *naiveEngine) replayAll(in *naiveInsured) {
	for i := 1; i < len(in.years); i++ {
		level := 0
		if !in.years[i].base {
			prev := in.years[i-1]
			liable := 0
			for _, c := range prev.claims {
				if c.ratio >= n.cfg.LiableThreshold {
					liable++
				}
			}
			level = naiveGrade(prev.level, liable, prev.protected, n.cfg.MaxLevel)
		}
		old := in.years[i].level
		if level < old {
			in.surcharges = append(in.surcharges, Surcharge{
				YearIndex: i,
				YearStart: in.years[i].start,
				OldLevel:  old,
				NewLevel:  level,
				Amount:    n.cfg.Premiums[level] - n.cfg.Premiums[old],
			})
		}
		in.years[i].level = level
	}
}

func (n *naiveEngine) Insure(insuredID, policyID string, day int) error {
	const op = "Insure"
	if insuredID == "" || policyID == "" || day < 0 {
		return fail(op, CodeInvalidParam, "")
	}
	if day < n.now {
		return fail(op, CodeClockRollback, "")
	}
	in, ok := n.ins[insuredID]
	if ok {
		last := in.years[len(in.years)-1]
		if day < last.start+policyYearDays {
			return fail(op, CodeActivePolicyExists, "")
		}
	} else {
		in = &naiveInsured{}
		n.ins[insuredID] = in
	}
	in.years = append(in.years, naiveYear{start: day, base: true})
	n.now = day
	return nil
}

func (n *naiveEngine) Renew(insuredID string, day int) (int, error) {
	const op = "Renew"
	if insuredID == "" || day < 0 {
		return -1, fail(op, CodeInvalidParam, "")
	}
	in, ok := n.ins[insuredID]
	if !ok {
		return -1, fail(op, CodeInsuredNotFound, "")
	}
	if day < n.now {
		return -1, fail(op, CodeClockRollback, "")
	}
	last := in.years[len(in.years)-1]
	expiry := last.start + policyYearDays
	if day < expiry-earlyRenewalDays {
		return -1, fail(op, CodeOutsideRenewalWindow, "")
	}
	level := 0
	if day <= expiry+n.cfg.RenewalGraceDays {
		liable := 0
		for _, c := range last.claims {
			if c.ratio >= n.cfg.LiableThreshold {
				liable++
			}
		}
		level = naiveGrade(last.level, liable, last.protected, n.cfg.MaxLevel)
		in.years = append(in.years, naiveYear{start: expiry, level: level})
	} else {
		in.years = append(in.years, naiveYear{start: day, base: true})
	}
	n.now = day
	return level, nil
}

func (n *naiveEngine) ReportClaim(insuredID, accidentID string, accidentDay, ratio, day int) error {
	const op = "ReportClaim"
	if insuredID == "" || accidentID == "" || day < 0 || accidentDay < 0 ||
		ratio < 0 || ratio > 100 || accidentDay > day {
		return fail(op, CodeInvalidParam, "")
	}
	in, ok := n.ins[insuredID]
	if !ok {
		return fail(op, CodeInsuredNotFound, "")
	}
	if day < n.now {
		return fail(op, CodeClockRollback, "")
	}
	for _, y := range in.years {
		for _, c := range y.claims {
			if c.id == accidentID {
				return fail(op, CodeClaimExists, "")
			}
		}
	}
	yi := n.findYear(in, accidentDay)
	if yi < 0 {
		return fail(op, CodeAccidentNotCovered, "")
	}
	in.years[yi].claims = append(in.years[yi].claims, claimRec{id: accidentID, day: accidentDay, ratio: ratio})
	n.replayAll(in)
	n.now = day
	return nil
}

func (n *naiveEngine) WithdrawClaim(insuredID, accidentID string, day int) error {
	const op = "WithdrawClaim"
	if insuredID == "" || accidentID == "" || day < 0 {
		return fail(op, CodeInvalidParam, "")
	}
	in, ok := n.ins[insuredID]
	if !ok {
		return fail(op, CodeInsuredNotFound, "")
	}
	if day < n.now {
		return fail(op, CodeClockRollback, "")
	}
	for yi := range in.years {
		for ci, c := range in.years[yi].claims {
			if c.id == accidentID {
				in.years[yi].claims = append(in.years[yi].claims[:ci], in.years[yi].claims[ci+1:]...)
				n.replayAll(in)
				n.now = day
				return nil
			}
		}
	}
	return fail(op, CodeClaimNotFound, "")
}

func (n *naiveEngine) BuyProtection(insuredID string, day int) error {
	const op = "BuyProtection"
	if insuredID == "" || day < 0 {
		return fail(op, CodeInvalidParam, "")
	}
	in, ok := n.ins[insuredID]
	if !ok {
		return fail(op, CodeInsuredNotFound, "")
	}
	if day < n.now {
		return fail(op, CodeClockRollback, "")
	}
	yi := n.findYear(in, day)
	if yi < 0 {
		return fail(op, CodeInvalidParam, "")
	}
	y := &in.years[yi]
	if y.level < n.cfg.ProtectStartLevel {
		return fail(op, CodeLevelInsufficient, "")
	}
	if y.protected {
		return fail(op, CodeAlreadyProtected, "")
	}
	y.protected = true
	n.replayAll(in)
	n.now = day
	return nil
}

func (n *naiveEngine) Transfer(insuredID, newPolicyID string, day int) error {
	const op = "Transfer"
	if insuredID == "" || newPolicyID == "" || day < 0 {
		return fail(op, CodeInvalidParam, "")
	}
	if _, ok := n.ins[insuredID]; !ok {
		return fail(op, CodeInsuredNotFound, "")
	}
	if day < n.now {
		return fail(op, CodeClockRollback, "")
	}
	n.now = day
	return nil
}

func (n *naiveEngine) level(insuredID string) (int, bool) {
	in, ok := n.ins[insuredID]
	if !ok {
		return -1, false
	}
	return in.years[len(in.years)-1].level, true
}

func (n *naiveEngine) yearLevels(insuredID string) []int {
	in, ok := n.ins[insuredID]
	if !ok {
		return nil
	}
	out := make([]int, len(in.years))
	for i, y := range in.years {
		out[i] = y.level
	}
	return out
}

func (n *naiveEngine) allSurcharges(insuredID string) []Surcharge {
	in, ok := n.ins[insuredID]
	if !ok {
		return nil
	}
	return in.surcharges
}
