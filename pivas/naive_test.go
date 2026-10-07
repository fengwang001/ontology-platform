package pivas_test

// 独立朴素模型：不复索引、不清除已完成批次、线性扫描，
// 用于与优化实现逐操作对照。语义与优化实现一致，但实现路径完全独立。

import (
	"fmt"
	"sort"
	"strings"

	"ontology/pivas"
)

type naiveError struct{ code pivas.Code }

func (e *naiveError) Error() string { return fmt.Sprintf("naive: %d", e.code) }

type naiveOrder struct {
	rec       pivas.Order
	roomMin   int64
	coldMin   int64
	storage   pivas.Storage
	transport int64
	benchID   string
	batchID   string
	cancelled bool
}

type naiveBatch struct {
	id         string
	benchID    string
	solvent    string
	lightProof bool
	start      int64
	orderIDs   []string
	removed    bool // 取消导致空批次被移除
}

type naiveBench struct {
	cfg     pivas.BenchConfig
	batches []*naiveBatch // 保留全部历史批次，从不清除
}

type naiveCenter struct {
	hasNow    bool
	lastNow   int64
	drugs     map[string]pivas.Drug
	pairs     [][2]string // 线性扫描，判定开销随配对总数增长
	benches   map[string]*naiveBench
	orders    map[string]*naiveOrder
	transport [2]int64
	batchSeq  int64
}

func newNaiveCenter(roomT, coldT int64) *naiveCenter {
	n := &naiveCenter{
		drugs:   make(map[string]pivas.Drug),
		benches: make(map[string]*naiveBench),
		orders:  make(map[string]*naiveOrder),
	}
	n.transport[pivas.StorageRoom] = roomT
	n.transport[pivas.StorageCold] = coldT
	return n
}

func (n *naiveCenter) checkClock(now int64) error {
	if now < 0 || now > pivas.MaxNow {
		return &naiveError{pivas.CodeInvalidParam}
	}
	if n.hasNow && now < n.lastNow {
		return &naiveError{pivas.CodeClockRollback}
	}
	return nil
}

func (n *naiveCenter) hasPair(a, b string) bool {
	for _, p := range n.pairs {
		if (p[0] == a && p[1] == b) || (p[0] == b && p[1] == a) {
			return true
		}
	}
	return false
}

func (n *naiveCenter) upsertDrug(now int64, d pivas.Drug) error {
	if d.ID == "" || d.SolventClass == "" || d.RoomStableSec <= 0 || d.ColdStableSec <= 0 {
		return &naiveError{pivas.CodeInvalidParam}
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.drugs[d.ID] = d
	n.lastNow, n.hasNow = now, true
	return nil
}

func (n *naiveCenter) addPair(now int64, a, b string) error {
	if a == "" || b == "" || a == b {
		return &naiveError{pivas.CodeInvalidParam}
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if !n.hasPair(a, b) {
		n.pairs = append(n.pairs, [2]string{a, b})
	}
	n.lastNow, n.hasNow = now, true
	return nil
}

func (n *naiveCenter) removePair(now int64, a, b string) error {
	if a == "" || b == "" || a == b {
		return &naiveError{pivas.CodeInvalidParam}
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	out := n.pairs[:0]
	for _, p := range n.pairs {
		if (p[0] == a && p[1] == b) || (p[0] == b && p[1] == a) {
			continue
		}
		out = append(out, p)
	}
	n.pairs = out
	n.lastNow, n.hasNow = now, true
	return nil
}

func (n *naiveCenter) registerBench(now int64, cfg pivas.BenchConfig) error {
	if cfg.ID == "" || cfg.Capacity <= 0 || cfg.ClearanceSec <= 0 || len(cfg.DurationByCount) != cfg.Capacity+1 {
		return &naiveError{pivas.CodeInvalidParam}
	}
	for i := 1; i <= cfg.Capacity; i++ {
		if cfg.DurationByCount[i] <= 0 || (i > 1 && cfg.DurationByCount[i] <= cfg.DurationByCount[i-1]) {
			return &naiveError{pivas.CodeInvalidParam}
		}
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if _, dup := n.benches[cfg.ID]; dup {
		return &naiveError{pivas.CodeInvalidParam}
	}
	n.benches[cfg.ID] = &naiveBench{cfg: cfg}
	n.lastNow, n.hasNow = now, true
	return nil
}

func (nb *naiveBench) end(b *naiveBatch) int64 {
	return b.start + nb.cfg.DurationByCount[len(b.orderIDs)]
}

// liveBatches 返回未移除（含已完成）批次，按创建顺序。
func (nb *naiveBench) liveBatches() []*naiveBatch {
	out := make([]*naiveBatch, 0, len(nb.batches))
	for _, b := range nb.batches {
		if !b.removed {
			out = append(out, b)
		}
	}
	return out
}

type naiveCandidate struct {
	delivery int64
	benchID  string
	isNew    bool
	batchPos int
	storage  pivas.Storage
	start    int64
	shifts   []int64
}

func naiveLess(x, y naiveCandidate) bool {
	if x.delivery != y.delivery {
		return x.delivery < y.delivery
	}
	if x.benchID != y.benchID {
		return x.benchID < y.benchID
	}
	if x.isNew != y.isNew {
		return !x.isNew
	}
	return x.batchPos < y.batchPos
}

func (n *naiveCenter) admit(now int64, o pivas.Order) (pivas.Admission, error) {
	// 1. 参数非法
	if o.ID == "" || len(o.DrugIDs) < 1 || len(o.DrugIDs) > 6 || o.Solvent == "" ||
		o.RequiredAt < 0 || o.RequiredAt > pivas.MaxNow {
		return pivas.Admission{}, &naiveError{pivas.CodeInvalidParam}
	}
	seen := map[string]bool{}
	for _, id := range o.DrugIDs {
		if id == "" || seen[id] {
			return pivas.Admission{}, &naiveError{pivas.CodeInvalidParam}
		}
		seen[id] = true
	}
	if _, dup := n.orders[o.ID]; dup {
		return pivas.Admission{}, &naiveError{pivas.CodeInvalidParam}
	}
	// 2. 时钟回退
	if err := n.checkClock(now); err != nil {
		return pivas.Admission{}, err
	}
	// 3. 药品不存在
	roomMin, coldMin := int64(-1), int64(-1)
	needLight := false
	for _, id := range o.DrugIDs {
		d, ok := n.drugs[id]
		if !ok {
			return pivas.Admission{}, &naiveError{pivas.CodeDrugNotFound}
		}
		if roomMin < 0 || d.RoomStableSec < roomMin {
			roomMin = d.RoomStableSec
		}
		if coldMin < 0 || d.ColdStableSec < coldMin {
			coldMin = d.ColdStableSec
		}
		needLight = needLight || d.LightSensitive
	}
	// 4. 禁忌配对（线性扫描）
	for i := 0; i < len(o.DrugIDs); i++ {
		for j := i + 1; j < len(o.DrugIDs); j++ {
			if n.hasPair(o.DrugIDs[i], o.DrugIDs[j]) {
				return pivas.Admission{}, &naiveError{pivas.CodeIncompatiblePair}
			}
		}
	}
	// 5. 溶媒不兼容
	for _, id := range o.DrugIDs {
		if n.drugs[id].SolventClass != o.Solvent {
			return pivas.Admission{}, &naiveError{pivas.CodeSolventMismatch}
		}
	}
	// 6. 避光冲突
	if needLight && !o.LightProofBag {
		return pivas.Admission{}, &naiveError{pivas.CodeLightConflict}
	}

	// 7. 排程搜索
	stableOf := func(s pivas.Storage) int64 {
		if s == pivas.StorageCold {
			return coldMin
		}
		return roomMin
	}
	benchIDs := make([]string, 0, len(n.benches))
	for id := range n.benches {
		benchIDs = append(benchIDs, id)
	}
	sort.Strings(benchIDs)

	var best naiveCandidate
	found := false
	consider := func(cand naiveCandidate) {
		if !found || naiveLess(cand, best) {
			best, found = cand, true
		}
	}
	for _, benchID := range benchIDs {
		nb := n.benches[benchID]
		live := nb.liveBatches()
		for _, storage := range []pivas.Storage{pivas.StorageRoom, pivas.StorageCold} {
			transport := n.transport[storage]
			if transport >= stableOf(storage) {
				continue
			}
			for i, b := range live {
				if b.start <= now {
					continue
				}
				if b.solvent != o.Solvent || b.lightProof != o.LightProofBag {
					continue
				}
				if len(b.orderIDs) >= nb.cfg.Capacity {
					continue
				}
				newEnd := b.start + nb.cfg.DurationByCount[len(b.orderIDs)+1]
				delivery := newEnd + transport
				if delivery > o.RequiredAt {
					continue
				}
				if !n.onTime(b, newEnd) {
					continue
				}
				if o.Urgent {
					shifts, ok := n.cascade(nb, live, i, newEnd)
					if !ok {
						continue
					}
					consider(naiveCandidate{delivery, benchID, false, i, storage, 0, shifts})
				} else {
					if i+1 < len(live) && newEnd+nb.cfg.ClearanceSec > live[i+1].start {
						continue
					}
					consider(naiveCandidate{delivery, benchID, false, i, storage, 0, nil})
				}
			}
			start := now
			for _, b := range live {
				if t := nb.end(b) + nb.cfg.ClearanceSec; t > start {
					start = t
				}
			}
			if delivery := start + nb.cfg.DurationByCount[1] + transport; delivery <= o.RequiredAt {
				consider(naiveCandidate{delivery, benchID, true, len(live), storage, start, nil})
			}
		}
	}
	if !found {
		return pivas.Admission{}, &naiveError{pivas.CodeNoFeasibleSlot}
	}

	// 落实
	rec := &naiveOrder{
		rec:       o,
		roomMin:   roomMin,
		coldMin:   coldMin,
		storage:   best.storage,
		transport: n.transport[best.storage],
		benchID:   best.benchID,
	}
	nb := n.benches[best.benchID]
	live := nb.liveBatches()
	var ba *naiveBatch
	if best.isNew {
		n.batchSeq++
		ba = &naiveBatch{
			id:         fmt.Sprintf("B%d", n.batchSeq),
			benchID:    best.benchID,
			solvent:    o.Solvent,
			lightProof: o.LightProofBag,
			start:      best.start,
		}
		nb.batches = append(nb.batches, ba)
	} else {
		ba = live[best.batchPos]
		for j, s := range best.shifts {
			live[best.batchPos+1+j].start = s
		}
	}
	ba.orderIDs = append(ba.orderIDs, o.ID)
	rec.batchID = ba.id
	n.orders[o.ID] = rec
	n.lastNow, n.hasNow = now, true
	return pivas.Admission{
		OrderID:  o.ID,
		BenchID:  best.benchID,
		BatchID:  ba.id,
		Storage:  best.storage,
		Start:    ba.start,
		Finish:   nb.end(ba),
		Delivery: best.delivery,
	}, nil
}

func (n *naiveCenter) onTime(b *naiveBatch, newEnd int64) bool {
	for _, id := range b.orderIDs {
		r := n.orders[id]
		if newEnd+r.transport > r.rec.RequiredAt {
			return false
		}
	}
	return true
}

func (n *naiveCenter) cascade(nb *naiveBench, live []*naiveBatch, i int, newEnd int64) ([]int64, bool) {
	if !n.onTime(live[i], newEnd) {
		return nil, false
	}
	starts := make([]int64, len(live)-i-1)
	prevEnd := newEnd
	for j := i + 1; j < len(live); j++ {
		s := live[j].start
		if need := prevEnd + nb.cfg.ClearanceSec; s < need {
			s = need
		}
		starts[j-i-1] = s
		prevEnd = s + nb.cfg.DurationByCount[len(live[j].orderIDs)]
	}
	for j := i + 1; j < len(live); j++ {
		end := starts[j-i-1] + nb.cfg.DurationByCount[len(live[j].orderIDs)]
		if !n.onTime(live[j], end) {
			return nil, false
		}
	}
	return starts, true
}

func (n *naiveCenter) cancel(now int64, orderID string) error {
	if orderID == "" {
		return &naiveError{pivas.CodeInvalidParam}
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	rec, ok := n.orders[orderID]
	if !ok {
		return &naiveError{pivas.CodeOrderNotFound}
	}
	if rec.cancelled {
		return &naiveError{pivas.CodeStateConflict}
	}
	nb := n.benches[rec.benchID]
	var ba *naiveBatch
	for _, b := range nb.batches {
		if b.id == rec.batchID && !b.removed {
			ba = b
			break
		}
	}
	if ba == nil || ba.start <= now {
		return &naiveError{pivas.CodeStateConflict}
	}
	out := ba.orderIDs[:0]
	for _, id := range ba.orderIDs {
		if id != orderID {
			out = append(out, id)
		}
	}
	ba.orderIDs = out
	rec.cancelled = true
	if len(ba.orderIDs) == 0 {
		ba.removed = true
	}
	n.lastNow, n.hasNow = now, true
	return nil
}

// dump 输出与优化实现完全相同的规范文本快照。
func (n *naiveCenter) dump() string {
	var sb strings.Builder
	last := int64(-1)
	if n.hasNow {
		last = n.lastNow
	}
	fmt.Fprintf(&sb, "lastNow=%d\n", last)
	benchIDs := make([]string, 0, len(n.benches))
	for id := range n.benches {
		benchIDs = append(benchIDs, id)
	}
	sort.Strings(benchIDs)
	for _, benchID := range benchIDs {
		nb := n.benches[benchID]
		for _, b := range nb.batches {
			if b.removed || nb.end(b) <= last {
				continue
			}
			fmt.Fprintf(&sb, "batch %s bench=%s start=%d end=%d solvent=%s lightProof=%v orders=[%s]\n",
				b.id, benchID, b.start, nb.end(b), b.solvent, b.lightProof, strings.Join(b.orderIDs, ","))
		}
	}
	ids := make([]string, 0, len(n.orders))
	for id := range n.orders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := n.orders[id]
		state := "scheduled"
		nb := n.benches[r.benchID]
		for _, b := range nb.batches {
			if b.id == r.batchID && nb.end(b) <= last {
				state = "completed"
			}
		}
		if r.cancelled {
			state = "cancelled"
		}
		fmt.Fprintf(&sb, "order %s state=%s bench=%s batch=%s storage=%s requiredAt=%d urgent=%v lightProof=%v\n",
			id, state, r.benchID, r.batchID, r.storage, r.rec.RequiredAt, r.rec.Urgent, r.rec.LightProofBag)
	}
	return sb.String()
}
