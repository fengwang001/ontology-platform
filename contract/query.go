package contract

// 性能验证用探针：统计一次有效值计算中检查的协议/撤销节点数。
// 生产路径默认关闭（metrics == nil）。
type queryMetrics struct {
	AmendmentChecks int
}

var activeMetrics *queryMetrics

// aliveAt 判断协议 a 在 day 日是否有效：
// 已生效、未被当时有效的撤销协议撤销。撤销可叠加（撤销的撤销恢复效力）。
// 仅遍历以 a 为根的撤销子树（revokerChildren 索引），
// 不扫描与本协议无关的其它协议，因此开销不随合同协议总数增长。
func aliveAt(a *amendment, children map[string][]*amendment, day int64) bool {
	if a == nil || a.effectiveDay < 0 || day < a.effectiveDay {
		return false
	}
	var isEffective func(r *amendment) bool
	isEffective = func(r *amendment) bool {
		if r == nil || r.effectiveDay < 0 || day < r.effectiveDay {
			return false
		}
		for _, x := range children[r.id] {
			if isEffective(x) {
				return false
			}
		}
		return true
	}
	for _, r := range children[a.id] {
		if isEffective(r) {
			return false
		}
	}
	return true
}

// effectiveValueAt 纯函数计算 day 日某条款有效值。
// candidates 为该条款被修改的全部协议（索引保证不随协议总数增长）。
func effectiveValueAt(clauseID string, mainValue int, mainExists bool, candidates []*amendment,
	children map[string][]*amendment, day int64) ValueSource {
	var best *amendment
	for _, cand := range candidates {
		if activeMetrics != nil {
			activeMetrics.AmendmentChecks++
		}
		if cand.effectiveDay < 0 || cand.effectiveDay > day {
			continue
		}
		if !aliveAt(cand, children, day) {
			continue
		}
		if best == nil || laterThan(cand, best) {
			best = cand
		}
	}
	if best == nil {
		if !mainExists {
			return ValueSource{Kind: "NONE"}
		}
		return ValueSource{Value: mainValue, Kind: "MAIN"}
	}
	return ValueSource{
		Value:        best.changes[clauseID],
		Kind:         "AMENDMENT",
		AmendmentID:  best.id,
		EffectiveDay: best.effectiveDay,
	}
}

// laterThan 判断取舍优先级：生效日晚者优先；同日完成时刻晚者优先；
// 再相同用创建序号兜底保证确定性。
func laterThan(a, b *amendment) bool {
	if a.effectiveDay != b.effectiveDay {
		return a.effectiveDay > b.effectiveDay
	}
	if a.completionSeq != b.completionSeq {
		return a.completionSeq > b.completionSeq
	}
	return a.createdSeq > b.createdSeq
}

// clauseValue 取 day 日某条款值（不存在返回 0,false）。
func (c *contractState) clauseValue(clause string, day int64) (int, bool) {
	if v, ok := c.clauses[clause]; ok {
		src := effectiveValueAt(clause, v, true, c.byClause[clause], c.revokerChildren, day)
		return src.Value, true
	}
	// 主合同没有，但仍可能有协议引入？本服务不允许引入新编号，故直接判定不存在。
	return 0, false
}

// expiryState 是对某观察日推演出的期限状态。
type expiryState struct {
	currentExpiry int64           // 观察日看到的到期日
	inTerm        bool            // 观察日是否在期
	terminated    bool            // 是否已（到期）终止
	records       []RenewalRecord // 截至观察日已发生的续签
}

// simulate 从主合同出发按时间边界逐个判定续签。
func (c *contractState) simulate(observeDay int64) expiryState {
	records := []RenewalRecord{}
	expiry := c.expiryDay

	for {
		var autoRenew bool
		var termLen, noticeDays int64
		// 取该到期日当日有效的续签条款。
		if v, ok := c.clauseValue(ClauseAutoRenew, expiry); ok {
			autoRenew = v == 1
		}
		if v, ok := c.clauseValue(ClauseRenewalTerm, expiry); ok {
			termLen = int64(v)
		}
		if v, ok := c.clauseValue(ClauseNoticeDays, expiry); ok {
			noticeDays = int64(v)
		}

		// 及时的不续签通知：在到期日之前至少提前 noticeDays 天被接受（含恰等）。
		timely := false
		for _, n := range c.notices {
			if n.day <= expiry-noticeDays {
				timely = true
				break
			}
		}

		// 提前终止：终止日不晚于本到期日时，合同在 termDay 日终止。
		if c.termDay >= 0 && c.termDay <= expiry {
			inTerm := observeDay >= c.startDay && observeDay <= c.termDay
			return expiryState{currentExpiry: expiry, inTerm: inTerm,
				terminated: observeDay > c.termDay, records: records}
		}

		// 观察日尚未跨过本到期日：续签/到期终止在到期日次日才改变状态。
		if observeDay <= expiry {
			return expiryState{currentExpiry: expiry,
				inTerm:  observeDay >= c.startDay && observeDay <= expiry,
				records: records}
		}

		if !autoRenew || timely || termLen <= 0 {
			return expiryState{currentExpiry: expiry, inTerm: false, terminated: true, records: records}
		}

		next := expiry + int64(termLen)
		rec := RenewalRecord{FromExpiryDay: expiry, NewExpiryDay: next, TermLength: int(termLen)}
		records = append(records, rec)
		expiry = next
	}
}
