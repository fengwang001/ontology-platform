package staffingtest

// 朴素模型用“深拷贝校验、失败丢弃副本”的方式工作，
// 天然满足“被拒绝操作不改变任何状态”，也与真实服务的事务回滚等价。

type workState struct {
	offers    map[int64]*mOffer
	lastBlock map[string]map[string]int
	excRemain map[string]int
	onboarded map[string]int
	pending   map[string]int
	candPen   map[string]int64
	candOnb   map[string]int64
}

func (m *Model) beginWork() *workState {
	w := &workState{
		offers:    map[int64]*mOffer{},
		lastBlock: map[string]map[string]int{},
		excRemain: map[string]int{},
		onboarded: map[string]int{},
		pending:   map[string]int{},
		candPen:   map[string]int64{},
		candOnb:   map[string]int64{},
	}
	for id, o := range m.offers {
		cp := *o
		w.offers[id] = &cp
	}
	for c, b := range m.lastBlock {
		nb := map[string]int{}
		for p, d := range b {
			nb[p] = d
		}
		w.lastBlock[c] = nb
	}
	for id, e := range m.exceptions {
		w.excRemain[id] = e.remaining
	}
	for _, o := range w.offers {
		switch {
		case o.status == MOnboarded && o.leftAt < 0:
			w.onboarded[o.position]++
			w.candOnb[o.candidate] = o.id
		case o.status == MPending || o.status == MAccepted:
			w.pending[o.position]++
			w.candPen[o.candidate] = o.id
		}
	}
	return w
}

func (w *workState) settleOffer(o *mOffer, now, grace int) {
	switch o.status {
	case MPending:
		if now > o.deadline {
			o.status = MExpired
			w.pending[o.position]--
			delete(w.candPen, o.candidate)
		}
	case MAccepted:
		if now > o.entryDate+grace {
			o.status = MAbandoned
			o.respondedAt = now
			w.pending[o.position]--
			delete(w.candPen, o.candidate)
			b := w.lastBlock[o.candidate]
			if b == nil {
				b = map[string]int{}
				w.lastBlock[o.candidate] = b
			}
			b[o.position] = now
		}
	}
}

func (w *workState) settlePosition(pos string, now, grace int) {
	ids := make([]int64, 0)
	for id, o := range w.offers {
		if o.position == pos && (o.status == MPending || o.status == MAccepted) {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		w.settleOffer(w.offers[id], now, grace)
	}
}

func (w *workState) settleCandidate(c string, now, grace int) {
	if id, ok := w.candPen[c]; ok {
		w.settleOffer(w.offers[id], now, grace)
	}
}

func (w *workState) inCooldown(m *Model, candidate, pos string, now int) bool {
	if b := w.lastBlock[candidate]; b != nil {
		if d, ok := b[pos]; ok && now-d < m.Cooldown {
			return true
		}
	}
	return false
}

func (m *Model) commitWork(w *workState, now int, created []*mOffer) {
	for _, o := range created {
		w.offers[o.id] = o
	}
	m.offers = w.offers
	m.lastBlock = w.lastBlock
	for id, rem := range w.excRemain {
		m.exceptions[id].remaining = rem
	}
	m.now, m.clockSet = now, true
}

func (m *Model) checkIssue(w *workState, now int, pos string, members []BatchMember, batch bool) Result {
	p, ok := m.positions[pos]
	if !ok {
		return singleErr(CNotFound)
	}
	for i, mb := range members {
		if !m.candidates[mb.Cand] {
			if batch {
				return batchErr(CNotFound, i)
			}
			return singleErr(CNotFound)
		}
	}
	if p.frozen {
		return singleErr(CFrozen)
	}

	w.settlePosition(pos, now, m.Grace)
	occ := w.onboarded[pos] + w.pending[pos]
	if occ+len(members) > p.headcount {
		idx := p.headcount - occ
		if idx < 0 {
			idx = 0
		}
		if batch {
			return batchErr(CFull, idx)
		}
		return singleErr(CFull)
	}

	quarter := quarterOf(now)
	remaining := 0
	var excID string
	for id, e := range m.exceptions {
		if e.position == pos && e.quarter == quarter {
			remaining = w.excRemain[id]
			excID = id
		}
	}
	var outIdx []int
	for i, mb := range members {
		if mb.Salary < p.bandLow || mb.Salary > p.bandHigh {
			outIdx = append(outIdx, i)
		}
	}
	if len(outIdx) > remaining {
		idx := outIdx[remaining]
		if batch {
			return batchErr(CBand, idx)
		}
		return singleErr(CBand)
	}

	for _, mb := range members {
		w.settleCandidate(mb.Cand, now, m.Grace)
	}
	seen := map[string]bool{}
	for i, mb := range members {
		if seen[mb.Cand] {
			if batch {
				return batchErr(CPending, i)
			}
			return singleErr(CPending)
		}
		seen[mb.Cand] = true
		if _, has := w.candPen[mb.Cand]; has {
			if batch {
				return batchErr(CPending, i)
			}
			return singleErr(CPending)
		}
	}

	for i, mb := range members {
		if w.inCooldown(m, mb.Cand, pos, now) {
			if batch {
				return batchErr(CCooldown, i)
			}
			return singleErr(CCooldown)
		}
	}

	created := make([]*mOffer, 0, len(members))
	for _, mb := range members {
		m.nextID++
		useExc := mb.Salary < p.bandLow || mb.Salary > p.bandHigh
		o := &mOffer{
			id: m.nextID, candidate: mb.Cand, position: pos,
			salary: mb.Salary, deadline: mb.Deadline, issuedAt: now,
			status: MPending, respondedAt: -1, entryDate: -1,
			onboardedAt: -1, leftAt: -1, canceledAt: -1,
			usedException: useExc,
		}
		if useExc {
			w.excRemain[excID]--
		}
		created = append(created, o)
	}
	m.commitWork(w, now, created)

	ids := make([]int64, len(created))
	for i, o := range created {
		ids[i] = o.id
	}
	return Result{OK: true, Code: 0, Index: -1, IDs: ids}
}

func (m *Model) issue(op Op) Result {
	if op.Pos == "" || op.Cand == "" || op.Salary < 0 || op.Deadline < op.Now || op.Now < 0 {
		return singleErr(CInvalidParam)
	}
	if !m.clockOK(op.Now) {
		return singleErr(CClockRollback)
	}
	w := m.beginWork()
	return m.checkIssue(w, op.Now, op.Pos,
		[]BatchMember{{Cand: op.Cand, Salary: op.Salary, Deadline: op.Deadline}}, false)
}

func (m *Model) issueBatch(op Op) Result {
	if op.Now < 0 || op.Pos == "" {
		return singleErr(CInvalidParam)
	}
	if len(op.Batch) == 0 {
		return singleErr(CInvalidParam)
	}
	for i, mb := range op.Batch {
		if mb.Cand == "" || mb.Salary < 0 || mb.Deadline < op.Now {
			return batchErr(CInvalidParam, i)
		}
	}
	if !m.clockOK(op.Now) {
		return singleErr(CClockRollback)
	}
	w := m.beginWork()
	return m.checkIssue(w, op.Now, op.Pos, op.Batch, true)
}
