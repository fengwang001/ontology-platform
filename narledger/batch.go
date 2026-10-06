package narledger

import "strconv"

// batch 一个药品批号的账面记录。
type batch struct {
	drug     string
	lot      string
	qty      int64
	expireAt int64
	seq      int64
}

func (b *batch) key() string { return b.drug + "\x00" + b.lot }

func (b *batch) issuableAt(t int64) bool { return b.qty > 0 && t < b.expireAt }

func (b *batch) viewKey() string { return b.drug + "/" + b.lot }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// stockBook 药品批次账面。
type stockBook struct {
	batches   map[string]*batch // key -> batch（含库存已归零与已过期批次，账面保留）
	heaps     map[string]*batchHeap
	seq       int64
	received  map[string]int64 // 药品 -> 累计入库
	destroyed map[string]int64 // 药品 -> 累计销毁
}

func newStockBook() *stockBook {
	return &stockBook{
		batches:   make(map[string]*batch),
		heaps:     make(map[string]*batchHeap),
		received:  make(map[string]int64),
		destroyed: make(map[string]int64),
	}
}

func (s *stockBook) register(op string, drug, lot string, qty, expireAt, now int64) *OpError {
	if s.heaps[drug] == nil {
		s.heaps[drug] = newBatchHeap(drug)
	}
	key := (&batch{drug: drug, lot: lot}).key()
	if _, exists := s.batches[key]; exists {
		return opError(op, ErrState, "药品 %s 批号 %s 已存在", drug, lot)
	}
	s.seq++
	b := &batch{drug: drug, lot: lot, qty: qty, expireAt: expireAt, seq: s.seq}
	s.batches[key] = b
	s.received[drug] += qty
	if b.issuableAt(now) {
		s.heaps[drug].PushB(b)
	}
	return nil
}

func (s *stockBook) batch(drug, lot string) *batch {
	return s.batches[(&batch{drug: drug, lot: lot}).key()]
}

// heapFor 返回药品的可发批次堆（过期且库存为正的批次不在堆内）。
func (s *stockBook) heapFor(drug string) *batchHeap { return s.heaps[drug] }

// syncHeap 依据批次当前状态与时间，把它加入或移出可发堆。
func (s *stockBook) syncHeap(b *batch, now int64) {
	h := s.heaps[b.drug]
	if h == nil {
		h = newBatchHeap(b.drug)
		s.heaps[b.drug] = h
	}
	if b.issuableAt(now) {
		if !h.contains(b.key()) {
			h.PushB(b)
		}
		return
	}
	h.Remove(b.key())
}
