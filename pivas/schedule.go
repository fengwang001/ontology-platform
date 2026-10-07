package pivas

import "fmt"

// candidate 一个可行安排候选。
type candidate struct {
	delivery int64
	benchID  string
	isNew    bool
	batchIdx int // 既有批次在队列中的下标；新批次为当时的队列长度
	storage  Storage
	start    int64   // 新批次的开始时刻（仅 isNew 有效）
	shifts   []int64 // 紧急医嘱连锁顺延后，queue[batchIdx+1:] 的新开始时刻
}

// less 依次比较：送达最早、台编号小、既有批次优先于新开批次、队列下标小。
func (x candidate) less(y candidate) bool {
	if x.delivery != y.delivery {
		return x.delivery < y.delivery
	}
	if x.benchID != y.benchID {
		return x.benchID < y.benchID
	}
	if x.isNew != y.isNew {
		return !x.isNew
	}
	return x.batchIdx < y.batchIdx
}

// Admit 受理一张配置医嘱，给出可行安排；无可行安排则整张拒绝，
// 且不改变任何既有状态、批次编组与时钟。
func (c *Center) Admit(now int64, o Order) (Admission, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 1. 参数非法
	if err := validateOrder(o); err != nil {
		return Admission{}, err
	}
	if _, dup := c.orders[o.ID]; dup {
		return Admission{}, errf(CodeInvalidParam, "医嘱标识重复: %q", o.ID)
	}
	// 2. 时钟回退
	if err := c.checkClock(now); err != nil {
		return Admission{}, err
	}
	// 3. 药品不存在（同时按受理时目录快照稳定秒数与避光需求）
	roomStable, coldStable := int64(0), int64(0)
	needLight := false
	for i, id := range o.DrugIDs {
		d, ok := c.drugs[id]
		if !ok {
			return Admission{}, errf(CodeDrugNotFound, "药品 %q 未登记", id)
		}
		if i == 0 || d.RoomStableSec < roomStable {
			roomStable = d.RoomStableSec
		}
		if i == 0 || d.ColdStableSec < coldStable {
			coldStable = d.ColdStableSec
		}
		needLight = needLight || d.LightSensitive
	}
	// 4. 禁忌配对（哈希集合，判定开销与配对总数无关）
	for i := 0; i < len(o.DrugIDs); i++ {
		for j := i + 1; j < len(o.DrugIDs); j++ {
			if _, hit := c.pairs[canonPair(o.DrugIDs[i], o.DrugIDs[j])]; hit {
				return Admission{}, errf(CodeIncompatiblePair, "药品 %q 与 %q 为禁忌配对", o.DrugIDs[i], o.DrugIDs[j])
			}
		}
	}
	// 5. 溶媒不兼容：药品所用溶媒类别须都等于所选溶媒
	for _, id := range o.DrugIDs {
		if c.drugs[id].SolventClass != o.Solvent {
			return Admission{}, errf(CodeSolventMismatch, "药品 %q 溶媒类别 %q 与所选溶媒 %q 不兼容", id, c.drugs[id].SolventClass, o.Solvent)
		}
	}
	// 6. 避光冲突：含须避光药品而医嘱未使用避光外袋
	if needLight && !o.LightProofBag {
		return Admission{}, errf(CodeLightConflict, "医嘱含须避光药品，必须使用避光外袋")
	}

	rec := &orderRec{
		id:         o.ID,
		requiredAt: o.RequiredAt,
		urgent:     o.Urgent,
		roomStable: roomStable,
		coldStable: coldStable,
		solvent:    o.Solvent,
		lightProof: o.LightProofBag,
	}

	// 7. 无可行安排
	best, found, feasible := c.search(now, rec)
	if !found {
		return Admission{}, errf(CodeNoFeasibleSlot, "医嘱 %q 无可行安排（要求送达 %d）", o.ID, o.RequiredAt)
	}
	bn, ba := c.commit(rec, best)
	c.accept(now)

	return Admission{
		OrderID:  o.ID,
		BenchID:  best.benchID,
		BatchID:  ba.id,
		Storage:  best.storage,
		Start:    ba.start,
		Finish:   bn.end(ba),
		Delivery: best.delivery,
		Reason: fmt.Sprintf("存放=%s 台=%s 批次=%s：在 %d 个可行候选中送达最早（%d）",
			best.storage, best.benchID, ba.id, feasible, best.delivery),
	}, nil
}

func validateOrder(o Order) error {
	if o.ID == "" {
		return errf(CodeInvalidParam, "医嘱标识必须非空")
	}
	if len(o.DrugIDs) < 1 || len(o.DrugIDs) > 6 {
		return errf(CodeInvalidParam, "医嘱药品数量须为 1..6: %d", len(o.DrugIDs))
	}
	seen := make(map[string]struct{}, len(o.DrugIDs))
	for _, id := range o.DrugIDs {
		if id == "" {
			return errf(CodeInvalidParam, "药品标识必须非空")
		}
		if _, dup := seen[id]; dup {
			return errf(CodeInvalidParam, "医嘱内药品重复: %q", id)
		}
		seen[id] = struct{}{}
	}
	if o.Solvent == "" {
		return errf(CodeInvalidParam, "溶媒标识必须非空")
	}
	if o.RequiredAt < 0 || o.RequiredAt > MaxNow {
		return errf(CodeInvalidParam, "要求送达时刻超出范围 [0,%d]: %d", MaxNow, o.RequiredAt)
	}
	return nil
}

// search 在只读前提下枚举全部可行安排并返回最优候选；feasible 为可行候选总数。
func (c *Center) search(now int64, rec *orderRec) (candidate, bool, int) {
	var best candidate
	found := false
	feasible := 0
	consider := func(cand candidate) {
		feasible++
		if !found || cand.less(best) {
			best, found = cand, true
		}
	}
	for _, benchID := range c.benchOrder {
		bn := c.benches[benchID]
		for _, storage := range []Storage{StorageRoom, StorageCold} {
			transport := c.transport[storage]
			stable := stableOf(rec, storage)
			// 送达须发生在有效期内：完成时刻+运送 < 完成时刻+稳定秒数
			if transport >= stable {
				continue
			}
			rec.storage, rec.transport = storage, transport
			// 既有且尚未开始的批次
			for i, ba := range bn.queue {
				if ba.start <= now {
					continue // 已开始
				}
				if ba.solvent != rec.solvent || ba.lightProof != rec.lightProof {
					continue
				}
				if len(ba.orders) >= bn.cfg.Capacity {
					continue
				}
				newEnd := ba.start + bn.dur(len(ba.orders)+1)
				delivery := newEnd + transport
				if delivery > rec.requiredAt {
					continue
				}
				if !ordersOnTime(ba.orders, newEnd) {
					continue
				}
				if rec.urgent {
					shifts, ok := bn.cascade(i, newEnd)
					if !ok {
						continue
					}
					consider(candidate{delivery: delivery, benchID: benchID, batchIdx: i, storage: storage, shifts: shifts})
				} else {
					// 普通医嘱不得改变同台任何后续批次的开始时刻
					if i+1 < len(bn.queue) && newEnd+bn.cfg.ClearanceSec > bn.queue[i+1].start {
						continue
					}
					consider(candidate{delivery: delivery, benchID: benchID, batchIdx: i, storage: storage})
				}
			}
			// 新开批次：创建时取该台最早可行时刻
			start := now
			if bn.freeAt > start {
				start = bn.freeAt
			}
			end := start + bn.dur(1)
			if delivery := end + transport; delivery <= rec.requiredAt {
				consider(candidate{delivery: delivery, benchID: benchID, isNew: true, batchIdx: len(bn.queue), storage: storage, start: start})
			}
		}
	}
	return best, found, feasible
}

func stableOf(rec *orderRec, s Storage) int64 {
	if s == StorageCold {
		return rec.coldStable
	}
	return rec.roomStable
}

// ordersOnTime 判定批次结束时刻改为 newEnd 后，其内既有医嘱是否仍按时。
func ordersOnTime(orders []*orderRec, newEnd int64) bool {
	for _, r := range orders {
		if newEnd+r.transport > r.requiredAt {
			return false
		}
	}
	return true
}

// cascade 计算紧急医嘱加入 queue[i]（结束时刻变为 newEnd）后，
// 后续批次的连锁顺延；顺延后所有受影响医嘱仍须按时，否则不可行。
// 返回 queue[i+1:] 的新开始时刻。
func (b *bench) cascade(i int, newEnd int64) ([]int64, bool) {
	if !ordersOnTime(b.queue[i].orders, newEnd) {
		return nil, false
	}
	q := b.queue
	starts := make([]int64, len(q)-i-1)
	prevEnd := newEnd
	for j := i + 1; j < len(q); j++ {
		s := q[j].start
		if need := prevEnd + b.cfg.ClearanceSec; s < need {
			s = need
		}
		starts[j-i-1] = s
		prevEnd = s + b.dur(len(q[j].orders))
	}
	for j := i + 1; j < len(q); j++ {
		end := starts[j-i-1] + b.dur(len(q[j].orders))
		if !ordersOnTime(q[j].orders, end) {
			return nil, false
		}
	}
	return starts, true
}

// commit 应用最优候选：加入既有批次或新开批次，并落实连锁顺延。
// 返回所在台与批次。
func (c *Center) commit(rec *orderRec, cand candidate) (*bench, *batch) {
	bn := c.benches[cand.benchID]
	rec.storage = cand.storage
	rec.transport = c.transport[cand.storage]
	rec.benchID = cand.benchID
	if cand.isNew {
		c.batchSeq++
		ba := &batch{
			id:         fmt.Sprintf("B%d", c.batchSeq),
			solvent:    rec.solvent,
			lightProof: rec.lightProof,
			start:      cand.start,
		}
		ba.orders = append(ba.orders, rec)
		bn.queue = append(bn.queue, ba)
		bn.recomputeFreeAt()
		rec.batchID = ba.id
		c.orders[rec.id] = rec
		return bn, ba
	}
	ba := bn.queue[cand.batchIdx]
	ba.orders = append(ba.orders, rec)
	for j, s := range cand.shifts {
		bn.queue[cand.batchIdx+1+j].start = s
	}
	// 加入后批次时长增加（可能伴随顺延），重算队尾占据。
	bn.recomputeFreeAt()
	rec.batchID = ba.id
	c.orders[rec.id] = rec
	return bn, ba
}

// Cancel 取消尚未开始的医嘱：同批次时长随数量变化，批次不得提前开始；
// 已开始的医嘱报状态不符。
func (c *Center) Cancel(now int64, orderID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if orderID == "" {
		return errf(CodeInvalidParam, "医嘱标识必须非空")
	}
	if err := c.checkClock(now); err != nil {
		return err
	}
	rec, ok := c.orders[orderID]
	if !ok {
		return errf(CodeOrderNotFound, "医嘱 %q 不存在", orderID)
	}
	if rec.cancelled {
		return errf(CodeStateConflict, "医嘱 %q 已取消", orderID)
	}
	bn := c.benches[rec.benchID]
	idx, ba := -1, (*batch)(nil)
	for i, b := range bn.queue {
		if b.id == rec.batchID {
			idx, ba = i, b
			break
		}
	}
	if ba == nil || ba.start <= now {
		return errf(CodeStateConflict, "医嘱 %q 所在批次已开始", orderID)
	}
	for i, r := range ba.orders {
		if r.id == orderID {
			ba.orders = append(ba.orders[:i], ba.orders[i+1:]...)
			break
		}
	}
	rec.cancelled = true
	if len(ba.orders) == 0 {
		// 空批次移除；后续批次开始时刻保持不变（不得提前）。
		bn.queue = append(bn.queue[:idx], bn.queue[idx+1:]...)
		bn.recomputeFreeAt()
	} else if idx == len(bn.queue)-1 {
		// 队尾批次数量减少、结束提前，重算队尾占据。
		bn.recomputeFreeAt()
	}
	c.accept(now)
	return nil
}
