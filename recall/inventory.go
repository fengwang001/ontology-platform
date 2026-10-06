package recall

import "sort"

// dispenseRecord 记录一次发放。退药抵扣时严格按
// "时刻从早到晚、同刻按发放先后"依次消耗该患者本人的记录。
type dispenseRecord struct {
	seq      int
	at       int64
	patient  string
	qty      int
	returned int // 该次发放已被退药抵扣的数量
}

// batchLedger 是一个药品批次的库存与发放台账。
type batchLedger struct {
	drugID  string
	batchID string
	total   int // 入库总量，恒不变
	stock   map[string]int

	dispenses []*dispenseRecord // 按 (at, seq) 有序追加
	nextSeq   int
	// heads 为每个患者自己的抵扣游标（指向下一条可能未满的本人记录）。
	heads map[string]int
	// held 为每个患者该批次尚未退回的总量，供退药超量判定 O(1) 取用。
	held map[string]int
}

// inventoryModule 管理全部药品批次台账，按药品分桶隔离。
type inventoryModule struct {
	batches map[string]map[string]*batchLedger
}

func newInventory() *inventoryModule {
	return &inventoryModule{batches: map[string]map[string]*batchLedger{}}
}

func (m *inventoryModule) hasBatch(drugID, batchID string) bool {
	b, ok := m.batches[drugID]
	if !ok {
		return false
	}
	_, ok = b[batchID]
	return ok
}

func (m *inventoryModule) ledger(drugID, batchID string) *batchLedger {
	return m.batches[drugID][batchID]
}

func (m *inventoryModule) inbound(reqDrugID, reqBatchID string, qty int) {
	b := m.batches[reqDrugID]
	if b == nil {
		b = map[string]*batchLedger{}
		m.batches[reqDrugID] = b
	}
	b[reqBatchID] = &batchLedger{
		drugID:  reqDrugID,
		batchID: reqBatchID,
		total:   qty,
		stock:   map[string]int{Warehouse: qty},
		heads:   map[string]int{},
		held:    map[string]int{},
	}
}

// move 在位置之间移动数量，调用方必须已完成等级与库存判定。
func (l *batchLedger) move(from, to string, qty int) {
	l.stock[from] -= qty
	l.stock[to] += qty
}

// dispense 执行发放并追加发放记录，调用方必须已完成全部判定。
func (l *batchLedger) dispense(at int64, location, patient string, qty int) {
	l.stock[location] -= qty
	l.dispenses = append(l.dispenses, &dispenseRecord{
		seq:     l.nextSeq,
		at:      at,
		patient: patient,
		qty:     qty,
	})
	l.nextSeq++
	l.held[patient] += qty
}

// heldQty 返回某患者该批次尚未退回的总量。
func (l *batchLedger) heldQty(patient string) int {
	return l.held[patient]
}

// acceptReturn 按 FIFO（时刻早者优先、同刻按发放先后）抵扣该患者本人的退药。
// 调用方必须已确认 qty 不超过该患者尚未退回的总量。
func (l *batchLedger) acceptReturn(patient string, qty int) {
	i := l.heads[patient]
	remaining := qty
	for remaining > 0 {
		// 跳过非本人或已抵扣满的记录。
		for i < len(l.dispenses) &&
			(l.dispenses[i].patient != patient || l.dispenses[i].returned == l.dispenses[i].qty) {
			i++
		}
		rec := l.dispenses[i]
		take := rec.qty - rec.returned
		if take > remaining {
			take = remaining
		}
		rec.returned += take
		remaining -= take
		if rec.returned == rec.qty {
			i++ // 该记录已抵扣满，游标才越过它
		}
	}
	l.heads[patient] = i
	l.held[patient] -= qty
	l.stock[Warehouse] += qty // 退回药品只进入药库
}

// outstandingSince 按患者汇总发放时刻不早于 issueAt、且尚未退回的数量。
func (l *batchLedger) outstandingSince(issueAt int64) map[string]int {
	out := map[string]int{}
	for _, rec := range l.dispenses {
		if rec.at >= issueAt {
			if left := rec.qty - rec.returned; left > 0 {
				out[rec.patient] += left
			}
		}
	}
	return out
}

// stockSnapshot 返回数量大于 0 的各位置库存，按位置名排序。
func (l *batchLedger) stockSnapshot() []StockAt {
	out := make([]StockAt, 0, len(l.stock))
	for loc, q := range l.stock {
		if q > 0 {
			out = append(out, StockAt{Location: loc, Quantity: q})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Location < out[j].Location })
	return out
}

// drugBatches 返回某药品全部批次台账（按批号排序）。
func (m *inventoryModule) drugBatches(drugID string) []*batchLedger {
	b := m.batches[drugID]
	ids := make([]string, 0, len(b))
	for id := range b {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*batchLedger, 0, len(ids))
	for _, id := range ids {
		out = append(out, b[id])
	}
	return out
}
