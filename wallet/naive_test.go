package wallet

// naiveWallet 是严格按题面规则写成的「逐步朴素模拟」：
// 不做任何索引与排序优化，每一步都直接遍历批次/消费单，
// 用以与 Wallet 的实现做差分对照。
type naiveBatch struct {
	id       int64
	amount   int64
	exp      int64
	grantSeq int64
	remain   int64
	deducted int64
	refunded int64
}

type naiveEntry struct {
	batchID  int64
	deducted int64
	refunded int64
}

type naiveSpend struct {
	sid     int64
	entries []naiveEntry // 扣减次序：先扣的在前
}

type naiveWallet struct {
	lastNow   int64
	grantSeq  int64
	batches   []*naiveBatch
	spends    []*naiveSpend
	discarded int64
}

func newNaive() *naiveWallet { return &naiveWallet{} }

func (n *naiveWallet) findBatch(id int64) *naiveBatch {
	for _, b := range n.batches {
		if b.id == id {
			return b
		}
	}
	return nil
}

func (n *naiveWallet) findSpend(sid int64) *naiveSpend {
	for _, s := range n.spends {
		if s.sid == sid {
			return s
		}
	}
	return nil
}

func (n *naiveWallet) grant(id, amount, exp, now int64) Reason {
	if now < n.lastNow {
		return ReasonNowRegression
	}
	if amount <= 0 {
		return ReasonNonPositiveAmount
	}
	if n.findBatch(id) != nil {
		return ReasonDuplicateBatchID
	}
	if exp <= now {
		return ReasonGrantExpired
	}
	n.grantSeq++
	n.batches = append(n.batches, &naiveBatch{
		id:       id,
		amount:   amount,
		exp:      exp,
		grantSeq: n.grantSeq,
		remain:   amount,
	})
	n.lastNow = now
	return ""
}

// pickSpendBatches 朴素挑批：反复在全部批次中选「可消费里最优先」的那一个。
func (n *naiveWallet) pickSpendBatches(now int64) []*naiveBatch {
	var chosen []*naiveBatch
	used := make(map[int64]bool)
	for {
		var best *naiveBatch
		for _, b := range n.batches {
			if used[b.id] || now >= b.exp || b.remain <= 0 {
				continue
			}
			if best == nil ||
				b.exp < best.exp ||
				(b.exp == best.exp && b.grantSeq < best.grantSeq) {
				best = b
			}
		}
		if best == nil {
			return chosen
		}
		used[best.id] = true
		chosen = append(chosen, best)
	}
}

func (n *naiveWallet) spend(sid, amount, now int64) ([]DeductItem, Reason) {
	if now < n.lastNow {
		return nil, ReasonNowRegression
	}
	if amount <= 0 {
		return nil, ReasonNonPositiveAmount
	}
	if n.findSpend(sid) != nil {
		return nil, ReasonDuplicateSpendID
	}
	order := n.pickSpendBatches(now)
	var avail int64
	for _, b := range order {
		avail += b.remain
	}
	if avail < amount {
		return nil, ReasonInsufficientBalance
	}

	sp := &naiveSpend{sid: sid}
	items := make([]DeductItem, 0)
	need := amount
	for _, b := range order {
		if need == 0 {
			break
		}
		take := b.remain
		if take > need {
			take = need
		}
		b.remain -= take
		b.deducted += take
		need -= take
		sp.entries = append(sp.entries, naiveEntry{batchID: b.id, deducted: take})
		items = append(items, DeductItem{BatchID: b.id, Amount: take})
	}
	n.spends = append(n.spends, sp)
	n.lastNow = now
	return items, ""
}

func (n *naiveWallet) refund(sid, amount, now int64) ([]RefundItem, Reason) {
	if now < n.lastNow {
		return nil, ReasonNowRegression
	}
	if amount <= 0 {
		return nil, ReasonNonPositiveAmount
	}
	sp := n.findSpend(sid)
	if sp == nil {
		return nil, ReasonUnknownSpend
	}
	var refundable int64
	for i := range sp.entries {
		refundable += sp.entries[i].deducted - sp.entries[i].refunded
	}
	if amount > refundable {
		return nil, ReasonRefundTooLarge
	}

	items := make([]RefundItem, 0)
	need := amount
	// 逆序：从最后扣的批次退起。
	for i := len(sp.entries) - 1; i >= 0 && need > 0; i-- {
		entry := &sp.entries[i]
		can := entry.deducted - entry.refunded
		if can <= 0 {
			continue
		}
		take := can
		if take > need {
			take = need
		}
		b := n.findBatch(entry.batchID)
		voided := now >= b.exp
		entry.refunded += take
		b.refunded += take
		b.remain += take
		if voided {
			n.discarded += take
		}
		need -= take
		items = append(items, RefundItem{BatchID: b.id, Amount: take, Voided: voided})
	}
	n.lastNow = now
	return items, ""
}

func (n *naiveWallet) balance(now int64) (int64, bool) {
	if now < n.lastNow {
		return 0, false
	}
	var total int64
	for _, b := range n.batches {
		if now < b.exp {
			total += b.remain
		}
	}
	n.lastNow = now
	return total, true
}

// invariant 校验朴素模型内部的恒等式：
// 每批 remain + deducted - refunded == 发放量。
func (n *naiveWallet) invariant() bool {
	for _, b := range n.batches {
		if b.remain+b.deducted-b.refunded != b.amount {
			return false
		}
	}
	return true
}
