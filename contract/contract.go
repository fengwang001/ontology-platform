package contract

import "math"

// Amendment 记录一份补充协议的全部状态。
type Amendment struct {
	id           int
	declared     int         // 声明生效日
	mods         map[int]int // 条款修改；撤销协议为 nil
	revokeTarget int         // 被撤销协议编号；修改类协议为 -1
	locked       bool        // 是否触及锁定类条款（须法务会签）

	signDays    map[string]int // 各方签署日
	firstSign   int            // 首方签署日，未签为 -1
	complete    bool           // 双方是否均已签署
	completeDay int            // 签署完成日（第二方签署日）
	signSeq     int64          // 签署完成时的全局操作序号（同日并列裁决用）

	needsCountersign bool // 是否需要法务会签
	countersigned    bool
	countersignDay   int

	effective bool // 是否已满足全部生效条件
	effDay    int  // 实际生效日 = max(声明生效日, 签署完成日[, 会签日])
}

// isRevocation 报告本协议是否为撤销协议。
func (a *Amendment) isRevocation() bool { return a.revokeTarget >= 0 }

// notice 是一条被接受的不续签通知。
type notice struct {
	day   int
	party string
}

// Contract 是一份合同的全部可变状态。
type Contract struct {
	id       int
	parties  [2]string
	master   map[int]int
	locked   map[int]bool
	start    int
	expiry   int // 初始到期日
	autoID   int // 自动续签开关条款
	periodID int // 续签期长度条款
	noticeID int // 通知提前天数条款
	signWin  int

	amendments map[int]*Amendment
	nextAID    int
	revsOf     map[int][]int // 协议编号 -> 以它为撤销目标的协议编号列表

	notices []notice

	terminated bool
	termDay    int

	timelines map[int]*clauseTimeline

	// 惰性续签游标：仅缓存“已最终确定”的判定（见 renewal.go）。
	expCursor      int // 当前已知的最近到期日
	done           bool
	naturalEnd     bool
	endDay         int
	noticeBoundary int       // 本任期通知窗口下界（上一到期日；首任期为 MinInt）
	renewals       []Renewal // 已最终确定的续签记录
}

func newContract(id int, p ContractParams) *Contract {
	master := make(map[int]int, len(p.Clauses))
	for k, v := range p.Clauses {
		master[k] = v
	}
	locked := make(map[int]bool, len(p.Locked))
	for _, k := range p.Locked {
		locked[k] = true
	}
	return &Contract{
		id:             id,
		parties:        p.Parties,
		master:         master,
		locked:         locked,
		start:          p.Start,
		expiry:         p.Expiry,
		autoID:         p.AutoRenewClause,
		periodID:       p.PeriodClause,
		noticeID:       p.NoticeClause,
		signWin:        p.SignWindowDays,
		amendments:     map[int]*Amendment{},
		revsOf:         map[int][]int{},
		timelines:      map[int]*clauseTimeline{},
		expCursor:      p.Expiry,
		noticeBoundary: math.MinInt,
	}
}

// partyIndex 返回参与方下标，非合同双方返回 -1。
func (c *Contract) partyIndex(party string) int {
	for i, p := range c.parties {
		if p == party {
			return i
		}
	}
	return -1
}

// touchesLocked 报告一组修改是否触及锁定类条款。
func (c *Contract) touchesLocked(mods map[int]int) bool {
	for clause := range mods {
		if c.locked[clause] {
			return true
		}
	}
	return false
}

// addAmendment 登记一份新协议（修改类或撤销类），返回其编号。
func (c *Contract) addAmendment(declared int, mods map[int]int, revokeTarget int) *Amendment {
	a := &Amendment{
		id:           c.nextAID,
		declared:     declared,
		revokeTarget: revokeTarget,
		signDays:     map[string]int{},
		firstSign:    -1,
	}
	if revokeTarget >= 0 {
		a.needsCountersign = false
		a.countersigned = true
		c.revsOf[revokeTarget] = append(c.revsOf[revokeTarget], a.id)
	} else {
		a.mods = make(map[int]int, len(mods))
		for k, v := range mods {
			a.mods[k] = v
		}
		a.locked = c.touchesLocked(mods)
		a.needsCountersign = a.locked
		a.countersigned = !a.locked
		for clause := range mods {
			ct := c.timelines[clause]
			if ct == nil {
				ct = newClauseTimeline(clause, c.master[clause])
				c.timelines[clause] = ct
			}
			ct.touching = append(ct.touching, a.id)
		}
	}
	c.amendments[a.id] = a
	c.nextAID++
	return a
}

// signable 检查此刻是否可接受 party 的签署。
func (c *Contract) signable(a *Amendment, party string, now int) *Error {
	if a.complete {
		return errf(StateNotAllowed, "amendment %d already fully signed", a.id)
	}
	if _, ok := a.signDays[party]; ok {
		return errf(StateNotAllowed, "party %q already signed amendment %d", party, a.id)
	}
	return nil
}

// applySign 记录 party 在 now 的签署；若签署完成则尝试使其生效。
func (c *Contract) applySign(a *Amendment, party string, now int, seq int64) {
	a.signDays[party] = now
	if a.firstSign < 0 {
		a.firstSign = now
	}
	if len(a.signDays) == 2 {
		a.complete = true
		a.completeDay = now
		a.signSeq = seq
		c.tryActivate(a)
	}
}

// tryActivate 在生效条件齐备时计算实际生效日并传播时间线脏标记。
func (c *Contract) tryActivate(a *Amendment) {
	if a.effective || !a.complete || !a.countersigned {
		return
	}
	a.effective = true
	a.effDay = max(a.declared, a.completeDay)
	if a.needsCountersign {
		a.effDay = max(a.effDay, a.countersignDay)
	}
	if a.isRevocation() {
		// 撤销生效会翻转目标协议的撤销状态，并沿撤销链传播：
		// 目标、目标撤销的对象、再下一层……这些协议的修改类条款都要重建。
		for t := a.revokeTarget; t >= 0; t = c.amendments[t].revokeTarget {
			c.dirtyAmendmentClauses(t, a.effDay)
		}
	} else {
		c.dirtyAmendmentClauses(a.id, a.effDay)
	}
}

// dirtyAmendmentClauses 将协议 aid 修改的全部条款自 day 起标记为待重建。
func (c *Contract) dirtyAmendmentClauses(aid, day int) {
	a := c.amendments[aid]
	for clause := range a.mods {
		c.markDirty(clause, day)
	}
}

// applyCountersign 记录法务会签；若已签署完成则使其生效。
func (c *Contract) applyCountersign(a *Amendment, now int) {
	a.countersigned = true
	a.countersignDay = now
	c.tryActivate(a)
}

// applyTerminate 在 now 终止合同：终止日之后的协议一律不生效。
func (c *Contract) applyTerminate(now int) {
	c.terminated = true
	c.termDay = now
	for clause := range c.timelines {
		c.markDirty(clause, now+1)
	}
}
