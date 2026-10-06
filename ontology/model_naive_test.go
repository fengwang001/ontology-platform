package ontology

import "sort"

// 朴素模型：完全独立地按规格重放操作日志。
// 每次操作后，它从原始在住段与账单日志出发，全量重算“当前净额”以及每个已退出住户的
// 累计清算头寸；不维护任何增量净额，作为增量实现的对照基准。

type naiveSeg struct {
	room       int64
	start, end int64 // end==0 开放
}

type naiveResident struct {
	segs    []naiveSeg
	active  bool
	outDay  int64
	checkIn int64
}

type naiveBill struct {
	id               int64
	amount           int64
	startDay, endDay int64
	method           SplitMethod
	payer            int64 // landlordPayer 或正 ID
	entryDay         int64
	status           BillStatus
	adjudication     int64
	disputed         bool
	seq              int64
	origCS           []Contribution
	cached           []Contribution
}

type naiveModel struct {
	maxAmount     int64
	disputeWindow int64
	lastNow       int64
	clockInit     bool
	residents     map[int64]*naiveResident
	rooms         map[int64]int64
	bills         map[int64]*naiveBill
	billOrder     []int64
	net           map[pairKey]int64
	folded        map[int64]map[int64]int64
	supplCount    map[int64]int64
	disputedExit  map[int64]map[int64]bool // billID -> set(residentID)
	included      map[int64]map[int64]bool
	seq           int64
	outSeq        map[int64]int64
}

func newNaive(maxAmount, disputeWindow int64) *naiveModel {
	return &naiveModel{
		maxAmount:     maxAmount,
		disputeWindow: disputeWindow,
		residents:     map[int64]*naiveResident{},
		rooms:         map[int64]int64{},
		bills:         map[int64]*naiveBill{},
		net:           map[pairKey]int64{},
		folded:        map[int64]map[int64]int64{},
		supplCount:    map[int64]int64{},
		disputedExit:  map[int64]map[int64]bool{},
		included:      map[int64]map[int64]bool{},
		outSeq:        map[int64]int64{},
	}
}

func (m *naiveModel) naiveMark(id, billID int64) {
	x := m.included[id]
	if x == nil {
		x = map[int64]bool{}
		m.included[id] = x
	}
	x[billID] = true
}

// reattributeAll 在入住/换房后重算所有 active 账单，增量更新双方均在住的净额。
func (m *naiveModel) reattributeAll() {
	for _, bid := range m.billOrder {
		b := m.bills[bid]
		if b.status != BillActive {
			continue
		}
		// 朴素模型每次 contribs 现算；这里需要旧归属，先用当前在住状态无法直接得到，
		// 因此在账单上缓存上次归属（与争议快照独立）。
		oldCS := b.cached
		newCS := m.contribs(b)
		b.cached = newCS
		if b.payer == landlordPayer {
			continue
		}
		adj := func(cs []Contribution, sign int64) {
			for _, c := range cs {
				if c.ResidentID == 0 || c.ResidentID == b.payer || c.Amount == 0 {
					continue
				}
				if !m.residents[b.payer].active || !m.residents[c.ResidentID].active {
					continue
				}
				k := canonPair(b.payer, c.ResidentID)
				d := int64(1)
				if k.a != b.payer {
					d = -1
				}
				m.net[k] += sign * d * c.Amount
			}
		}
		adj(oldCS, -1)
		adj(newCS, 1)
	}
}

func naiveOverlap(aStart, aEnd, bStart, bEnd int64) bool {
	if aEnd != 0 && bStart >= aEnd {
		return false
	}
	if bEnd != 0 && aStart >= bEnd {
		return false
	}
	return true
}

func (m *naiveModel) checkIn(now, id, room, day, area int64) error {
	if now < 0 || id <= 0 || room <= 0 || day < 0 || day > now || area <= 0 {
		return ErrInvalid
	}
	if m.clockInit && now < m.lastNow {
		return ErrClockBack
	}
	if _, ok := m.residents[id]; ok {
		return ErrInvalid
	}
	if old, ok := m.rooms[room]; ok && old != area {
		return ErrInvalid
	}
	for _, r := range m.residents {
		for _, seg := range r.segs {
			if seg.room == room && naiveOverlap(day, 0, seg.start, seg.end) {
				return ErrOverlap
			}
		}
	}
	m.rooms[room] = area
	m.residents[id] = &naiveResident{
		segs:    []naiveSeg{{room: room, start: day}},
		active:  true,
		checkIn: day,
	}
	m.reattributeAll()
	m.lastNow, m.clockInit = now, true
	return nil
}

func (m *naiveModel) checkOut(now, id, outDay int64) error {
	if now < 0 || id <= 0 || outDay < 0 || outDay > now {
		return ErrInvalid
	}
	if m.clockInit && now < m.lastNow {
		return ErrClockBack
	}
	r, ok := m.residents[id]
	if !ok {
		return ErrNotFound
	}
	last := r.segs[len(r.segs)-1]
	if outDay < last.start {
		return ErrInvalid
	}
	if !r.active {
		return ErrState
	}
	// 闭合前先抓取旧归属（退出日当天仍在住口径），再关闭并重算。
	type oc struct {
		bid   int64
		oldCS []Contribution
		newCS []Contribution
		bill  *naiveBill
	}
	var pending []oc
	for _, bid := range m.billOrder {
		bill := m.bills[bid]
		if bill.status != BillActive {
			continue
		}
		oldCS := bill.cached
		if m.shareOf(oldCS, id) == 0 {
			continue
		}
		pending = append(pending, oc{bid: bid, oldCS: oldCS, bill: bill})
	}
	r.segs[len(r.segs)-1].end = outDay
	r.active = false
	r.outDay = outDay
	m.seq++
	m.outSeq[id] = m.seq
	for i := range pending {
		p := &pending[i]
		p.newCS = m.contribs(p.bill)
		p.bill.cached = p.newCS
		if p.bill.payer == landlordPayer {
			continue
		}
		collect := func(cs []Contribution, sign int64) {
			for _, c := range cs {
				if c.ResidentID == 0 || c.ResidentID == p.bill.payer {
					continue
				}
				if c.ResidentID == id || p.bill.payer == id {
					continue
				}
				if !m.residents[c.ResidentID].active || !m.residents[p.bill.payer].active {
					continue
				}
				k := canonPair(p.bill.payer, c.ResidentID)
				d := int64(1)
				if k.a != p.bill.payer {
					d = -1
				}
				m.net[k] += sign * d * c.Amount
			}
		}
		collect(p.oldCS, -1)
		collect(p.newCS, 1)
	}
	m.foldAtCheckout(id)
	// 记录退出时仍处于争议态的账单。
	for bid, b := range m.bills {
		if b.status == BillDisputed {
			set := m.disputedExit[bid]
			if set == nil {
				set = map[int64]bool{}
				m.disputedExit[bid] = set
			}
			set[id] = true
		}
	}
	m.lastNow = now
	return nil
}

func (m *naiveModel) changeRoom(now, id, newRoom, day, area int64) error {
	if now < 0 || id <= 0 || newRoom <= 0 || day < 0 || day > now || area <= 0 {
		return ErrInvalid
	}
	if m.clockInit && now < m.lastNow {
		return ErrClockBack
	}
	r, ok := m.residents[id]
	if !ok {
		return ErrNotFound
	}
	if old, ok := m.rooms[newRoom]; ok && old != area {
		return ErrInvalid
	}
	cur := r.segs[len(r.segs)-1]
	if day < cur.start || (!r.active && day >= cur.end) {
		return ErrInvalid
	}
	for _, o := range m.residents {
		if o == r {
			continue
		}
		for _, seg := range o.segs {
			if seg.room == newRoom && naiveOverlap(day, 0, seg.start, seg.end) {
				return ErrOverlap
			}
		}
	}
	if !r.active {
		return ErrState
	}
	if cur.room == newRoom {
		return ErrState
	}
	m.rooms[newRoom] = area
	r.segs[len(r.segs)-1].end = day
	r.segs = append(r.segs, naiveSeg{room: newRoom, start: day})
	m.reattributeAll()
	m.lastNow = now
	return nil
}

func (m *naiveModel) enterBill(now, id, amount int64, startDay, endDay int64, method SplitMethod, payerID int64, landlord bool) error {
	if now < 0 || id <= 0 || amount <= 0 || startDay < 0 || endDay <= startDay ||
		(method != SplitPerHead && method != SplitByArea) || (!landlord && payerID <= 0) {
		return ErrInvalid
	}
	if m.clockInit && now < m.lastNow {
		return ErrClockBack
	}
	if _, dup := m.bills[id]; dup {
		return ErrInvalid
	}
	if !landlord {
		if _, ok := m.residents[payerID]; !ok {
			return ErrNotFound
		}
	}
	if amount > m.maxAmount {
		return ErrAmount
	}
	payer := payerID
	if landlord {
		payer = landlordPayer
	}
	m.seq++
	m.bills[id] = &naiveBill{
		id: id, amount: amount, startDay: startDay, endDay: endDay,
		method: method, payer: payer, entryDay: now, status: BillActive, seq: m.seq,
	}
	m.billOrder = append(m.billOrder, id)
	m.bills[id].cached = m.contribs(m.bills[id])
	m.applyNewBill(m.bills[id])
	m.lastNow = now
	return nil
}

func (m *naiveModel) dispute(now, id int64) error {
	if now < 0 || id <= 0 {
		return ErrInvalid
	}
	if m.clockInit && now < m.lastNow {
		return ErrClockBack
	}
	b, ok := m.bills[id]
	if !ok {
		return ErrNotFound
	}
	if b.status == BillAdjudicated || b.disputed {
		return ErrState
	}
	if now > b.entryDay+m.disputeWindow {
		return ErrState
	}
	m.removeLive(b)
	b.origCS = m.contribs(b)
	b.status = BillDisputed
	b.disputed = true
	m.lastNow = now
	return nil
}

func (m *naiveModel) adjudicate(now, id, newAmount int64) error {
	if now < 0 || id <= 0 || newAmount < 0 {
		return ErrInvalid
	}
	if m.clockInit && now < m.lastNow {
		return ErrClockBack
	}
	b, ok := m.bills[id]
	if !ok {
		return ErrNotFound
	}
	if b.status != BillDisputed {
		return ErrState
	}
	if newAmount > b.amount {
		return ErrAmount
	}
	oldCS := b.origCS
	oldAmount := b.amount
	b.adjudication = newAmount
	b.amount = newAmount
	b.status = BillAdjudicated
	newCS := m.contribs(b)
	m.reapplyLive(b, newCS)
	m.supplementAdjudication(b, oldCS, oldAmount, newCS, now)
	m.lastNow = now
	return nil
}

// ---- 全量重算 ----

func (m *naiveModel) present(id int64, day int64) bool {
	for _, seg := range m.residents[id].segs {
		if day >= seg.start && (seg.end == 0 || day < seg.end) {
			return true
		}
	}
	return false
}

func (m *naiveModel) roomAt(id int64, day int64) (int64, bool) {
	for _, seg := range m.residents[id].segs {
		if day >= seg.start && (seg.end == 0 || day < seg.end) {
			return seg.room, true
		}
	}
	return 0, false
}

// contribs 全量重算某账单在当前在住历史下的归属。
func (m *naiveModel) contribs(b *naiveBill) []Contribution {
	ids := make([]int64, 0, len(m.residents))
	for id := range m.residents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	totals := map[int64]int64{}
	var landlord int64
	days := b.endDay - b.startDay
	base := b.amount / days
	rem := b.amount % days
	for day := b.startDay; day < b.endDay; day++ {
		dayTotal := base
		if day-b.startDay < rem {
			dayTotal++
		}
		type cand struct {
			id, room, weight, checkIn int64
		}
		cs := []cand{}
		var weightSum int64
		for _, id := range ids {
			room, ok := m.roomAt(id, day)
			if !ok {
				continue
			}
			w := int64(1)
			if b.method == SplitByArea {
				w = m.rooms[room]
			}
			cs = append(cs, cand{id: id, room: room, weight: w, checkIn: m.residents[id].checkIn})
			weightSum += w
		}
		if len(cs) == 0 {
			var bestID int64
			var bestAt int64
			var bestRoom int64
			found := false
			for _, id := range ids {
				at := m.residents[id].checkIn
				if at <= day {
					continue
				}
				room := m.residents[id].segs[0].room
				if !found || at < bestAt || (at == bestAt && room < bestRoom) {
					bestID, bestAt, bestRoom, found = id, at, room, true
				}
			}
			if found {
				totals[bestID] += dayTotal
			} else {
				landlord += dayTotal
			}
			continue
		}
		sort.SliceStable(cs, func(i, j int) bool {
			if cs[i].checkIn != cs[j].checkIn {
				return cs[i].checkIn < cs[j].checkIn
			}
			return cs[i].room < cs[j].room
		})
		per := dayTotal / weightSum
		left := dayTotal % weightSum
		var acc int64
		for i, c := range cs {
			lo := acc
			acc += c.weight
			hi := acc
			sh := per * c.weight
			if b.method == SplitPerHead {
				if int64(i) < left {
					sh++
				}
			} else {
				up := left
				if hi < up {
					up = hi
				}
				if up > lo {
					sh += up - lo
				}
			}
			totals[c.id] += sh
		}
	}
	out := []Contribution{}
	for _, id := range ids {
		if totals[id] > 0 {
			out = append(out, Contribution{ResidentID: id, Amount: totals[id]})
		}
	}
	if landlord > 0 {
		out = append(out, Contribution{ResidentID: 0, Amount: landlord})
	}
	return out
}

// ---- 朴素的独立簿记（事件规则的另一份实现）----

func (m *naiveModel) ensureFolded(id int64) map[int64]int64 {
	x := m.folded[id]
	if x == nil {
		x = map[int64]int64{}
		m.folded[id] = x
	}
	return x
}

func (m *naiveModel) foldAtCheckout(id int64) {
	fold := m.ensureFolded(id)
	csSeq := m.outSeq[id]
	byOther := map[int64]int64{}
	for _, bid := range m.billOrder {
		b := m.bills[bid]
		if b.status != BillActive || b.payer == landlordPayer || b.seq >= csSeq {
			continue
		}
		cs := m.contribs(b)
		if b.payer == id {
			hasReceivable := false
			for _, c := range cs {
				if c.ResidentID != 0 && c.ResidentID != id && c.Amount > 0 {
					byOther[c.ResidentID] += c.Amount
					hasReceivable = true
				}
			}
			if hasReceivable {
				m.naiveMark(id, bid)
			}
			continue
		}
		if m.included[id] != nil && m.included[id][bid] {
			continue
		}
		if sh := m.shareOf(cs, id); sh > 0 {
			byOther[b.payer] -= sh
			m.naiveMark(id, bid)
		}
	}
	for k := range m.net {
		if k.a == id || k.b == id {
			m.net[k] = 0
		}
	}
	for other, d := range byOther {
		fold[other] += d
	}
}

func (m *naiveModel) eachLivePair(b *naiveBill, cs []Contribution, fn func(payer, sharee, amount int64)) {
	if b.payer == landlordPayer {
		return
	}
	for _, c := range cs {
		if c.ResidentID == 0 || c.ResidentID == b.payer || c.Amount == 0 {
			continue
		}
		fn(b.payer, c.ResidentID, c.Amount)
	}
}

// applyNewBill 新账单：双方在住计入 net，否则计入已退出者的头寸（补充清算）。
func (m *naiveModel) applyNewBill(b *naiveBill) {
	cs := m.contribs(b)
	m.eachLivePair(b, cs, func(payer, sharee, amount int64) {
		pActive := m.residents[payer].active
		sActive := m.residents[sharee].active
		pBefore := !pActive && m.outSeq[payer] < b.seq
		sBefore := !sActive && m.outSeq[sharee] < b.seq
		if pActive && sActive {
			k := canonPair(payer, sharee)
			d := int64(1)
			if k.a != payer {
				d = -1
			}
			m.net[k] += d * amount
		}
		if sBefore {
			m.ensureFolded(sharee)[payer] -= amount
			m.naiveMark(sharee, b.id)
			m.supplCount[sharee]++
		}
		if pBefore && (sActive || sBefore) {
			m.ensureFolded(payer)[sharee] += amount
			m.naiveMark(payer, b.id)
			m.supplCount[payer]++
		}
	})
}

func (m *naiveModel) removeLive(b *naiveBill) {
	cs := m.contribs(b)
	m.eachLivePair(b, cs, func(payer, sharee, amount int64) {
		if !m.residents[payer].active || !m.residents[sharee].active {
			return
		}
		k := canonPair(payer, sharee)
		d := int64(1)
		if k.a != payer {
			d = -1
		}
		m.net[k] -= d * amount
	})
}

func (m *naiveModel) reapplyLive(b *naiveBill, newCS []Contribution) {
	m.eachLivePair(b, newCS, func(payer, sharee, amount int64) {
		if !m.residents[payer].active || !m.residents[sharee].active {
			return
		}
		k := canonPair(payer, sharee)
		d := int64(1)
		if k.a != payer {
			d = -1
		}
		m.net[k] += d * amount
	})
}

func (m *naiveModel) shareOf(cs []Contribution, id int64) int64 {
	for _, c := range cs {
		if c.ResidentID == id {
			return c.Amount
		}
	}
	return 0
}

// foldedHas 在朴素模型中表示“该住户的原清算是否曾包含此账单”：
// 以退出时刻该账单是否为 active 判定（争议中的账单不在原清算内）。
func (m *naiveModel) foldedHas(id, billID int64) bool {
	if pend := m.disputedExit[billID]; pend != nil && pend[id] {
		return false
	}
	return m.included[id] != nil && m.included[id][billID]
}

// supplementAdjudication 对所有当时已退出的住户按新旧有向贡献之差补充清算。
func (m *naiveModel) supplementAdjudication(b *naiveBill, oldCS []Contribution, oldAmount int64, newCS []Contribution, now int64) {
	if b.payer == landlordPayer {
		return
	}
	ids := map[int64]bool{}
	for id, r := range m.residents {
		if !r.active {
			ids[id] = true
		}
	}
	payerWasRelevant := false
	if pend := m.disputedExit[b.id]; pend != nil {
		for id := range pend {
			if id != b.payer && m.shareOf(newCS, id) == 0 {
				delete(pend, id)
				continue
			}
			if id == b.payer {
				for _, c := range newCS {
					if c.ResidentID != 0 && c.ResidentID != id && c.Amount > 0 {
						payerWasRelevant = true
					}
				}
				if !payerWasRelevant {
					delete(pend, id)
					continue
				}
			}
			ids[id] = true
		}
		if len(pend) == 0 {
			delete(m.disputedExit, b.id)
		}
	}
	// 争议期间退出者：其旧贡献按 0 计（原清算未包含该账单）。
	for id := range ids {
		if id == b.payer {
			sharees := map[int64]bool{}
			for _, c := range newCS {
				if c.ResidentID != 0 && c.ResidentID != id {
					sharees[c.ResidentID] = true
				}
			}
			for _, c := range oldCS {
				if c.ResidentID != 0 && c.ResidentID != id {
					sharees[c.ResidentID] = true
				}
			}
			for sharee := range sharees {
				oldShare := m.shareOf(oldCS, sharee)
				if pend := m.disputedExit[b.id]; pend != nil && pend[id] {
					oldShare = 0
				}
				d := m.shareOf(newCS, sharee) - oldShare
				if d != 0 {
					m.ensureFolded(id)[sharee] += d
					m.supplCount[id]++
				}
			}
			continue
		}
		newD := -m.shareOf(newCS, id)
		oldD := -m.shareOf(oldCS, id)
		if pend := m.disputedExit[b.id]; pend != nil && pend[id] {
			oldD = 0
		}
		if d := newD - oldD; d != 0 {
			m.ensureFolded(id)[b.payer] += d
			m.supplCount[id]++
		}
	}
}

func (m *naiveModel) liveNet() map[pairKey]int64 {
	out := map[pairKey]int64{}
	for k, v := range m.net {
		out[k] = v
	}
	return out
}
