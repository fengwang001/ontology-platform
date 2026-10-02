package scheduler

import (
	"container/heap"
	"errors"
	"math/big"
	"sync"
)

const (
	MaxOffset      = int64(1_000_000_000_000_000)
	MaxBatchSize   = int64(1_000_000)
	MaxDenominator = int64(1_000_000)
)

var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrBelowWatermark      = errors.New("below watermark")
	ErrIntervalNotFound    = errors.New("interval not found")
	ErrExhausted           = errors.New("interval exhausted")
	ErrCannotSplit         = errors.New("cannot split")
	ErrBatchNotFound       = errors.New("batch not found")
	ErrAlreadyAcknowledged = errors.New("batch already acknowledged")
)

type Offset = int64
type ID = int64

type AddResult struct {
	ID ID
}

type ProcessResult struct {
	Count   Offset
	From    Offset
	To      Offset
	BatchID ID
}

type SplitResult struct {
	ID ID
}

type Progress struct {
	From           Offset
	To             Offset
	Next           Offset
	Remaining      Offset
	PendingBatches int
}

type batch struct {
	id       ID
	interval ID
	from     Offset
	to       Offset
	acked    bool
}

type interval struct {
	id        ID
	from      Offset
	to        Offset
	next      Offset
	version   int64
	pending   int
	batchHeap batchHeap
}

type holdEntry struct {
	hold    Offset
	id      ID
	version int64
}

type holdHeap []holdEntry

type batchEntry struct {
	from Offset
	id   ID
}

type batchHeap []batchEntry

func (h holdHeap) Len() int           { return len(h) }
func (h holdHeap) Less(i, j int) bool { return h[i].hold < h[j].hold }
func (h holdHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *holdHeap) Push(x any)        { *h = append(*h, x.(holdEntry)) }
func (h *holdHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func (h batchHeap) Len() int           { return len(h) }
func (h batchHeap) Less(i, j int) bool { return h[i].from < h[j].from }
func (h batchHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *batchHeap) Push(x any)        { *h = append(*h, x.(batchEntry)) }
func (h *batchHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

type Scheduler struct {
	mu         sync.Mutex
	nextID     ID
	nextBatch  ID
	watermark  Offset
	probes     int64
	intervals  map[ID]*interval
	batches    map[ID]*batch
	globalHeap holdHeap
}

func NewScheduler() *Scheduler {
	return &Scheduler{
		intervals: make(map[ID]*interval),
		batches:   make(map[ID]*batch),
	}
}

func (s *Scheduler) Add(from, to Offset) (AddResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if from < 0 || to > MaxOffset || from >= to {
		return AddResult{}, ErrInvalidArgument
	}
	if from < s.watermark {
		return AddResult{}, ErrBelowWatermark
	}

	s.nextID++
	iv := &interval{
		id:      s.nextID,
		from:    from,
		to:      to,
		next:    from,
		version: 1,
	}
	s.intervals[iv.id] = iv
	heap.Push(&s.globalHeap, holdEntry{hold: from, id: iv.id, version: iv.version})
	s.refreshWatermark()
	return AddResult{ID: iv.id}, nil
}

func (s *Scheduler) Process(id ID, n Offset) (ProcessResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id <= 0 || n < 1 || n > MaxBatchSize {
		return ProcessResult{}, ErrInvalidArgument
	}
	iv := s.intervals[id]
	if iv == nil {
		return ProcessResult{}, ErrIntervalNotFound
	}
	if iv.next == iv.to {
		return ProcessResult{}, ErrExhausted
	}

	count := min64(n, iv.to-iv.next)
	from := iv.next
	to := from + count
	iv.next = to

	s.nextBatch++
	b := &batch{id: s.nextBatch, interval: id, from: from, to: to}
	s.batches[b.id] = b
	iv.pending++
	heap.Push(&iv.batchHeap, batchEntry{from: from, id: b.id})

	// hold 必须与领取前相同：查看区间堆顶确认仍由最早未确认批压住。
	s.probes++
	_ = iv.batchHeap[0].from

	return ProcessResult{Count: count, From: from, To: to, BatchID: b.id}, nil
}

func (s *Scheduler) Split(id ID, numerator, denominator Offset) (SplitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id <= 0 || numerator < 0 || denominator < 1 || denominator > MaxDenominator || numerator > denominator {
		return SplitResult{}, ErrInvalidArgument
	}
	iv := s.intervals[id]
	if iv == nil {
		return SplitResult{}, ErrIntervalNotFound
	}
	if iv.next == iv.to {
		return SplitResult{}, ErrExhausted
	}

	rem := iv.to - iv.next
	product := new(big.Int).Mul(big.NewInt(rem), big.NewInt(numerator))
	keep := ceilDivBig(product, denominator)
	if keep < 1 {
		keep = 1
	}
	splitPoint := iv.next + keep
	if splitPoint >= iv.to {
		return SplitResult{}, ErrCannotSplit
	}

	originalTo := iv.to
	iv.to = splitPoint

	s.nextID++
	child := &interval{
		id:      s.nextID,
		from:    splitPoint,
		to:      originalTo,
		next:    splitPoint,
		version: 1,
	}
	s.intervals[child.id] = child
	heap.Push(&s.globalHeap, holdEntry{hold: splitPoint, id: child.id, version: child.version})
	return SplitResult{ID: child.id}, nil
}

func (s *Scheduler) Ack(batchID ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if batchID <= 0 {
		return ErrInvalidArgument
	}
	b := s.batches[batchID]
	if b == nil {
		return ErrBatchNotFound
	}
	if b.acked {
		return ErrAlreadyAcknowledged
	}

	iv := s.intervals[b.interval]
	b.acked = true
	iv.pending--

	if iv.batchHeap.Len() > 0 && iv.batchHeap[0].id == batchID {
		s.cleanIntervalHeap(iv)
		if iv.pending == 0 && iv.next == iv.to {
			iv.version++
		} else {
			iv.version++
			heap.Push(&s.globalHeap, holdEntry{hold: intervalHold(iv), id: iv.id, version: iv.version})
		}
		s.refreshWatermark()
	}
	return nil
}

func (s *Scheduler) Progress(id ID) (Progress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id <= 0 {
		return Progress{}, ErrIntervalNotFound
	}
	iv := s.intervals[id]
	if iv == nil {
		return Progress{}, ErrIntervalNotFound
	}
	return Progress{
		From:           iv.from,
		To:             iv.to,
		Next:           iv.next,
		Remaining:      iv.to - iv.next,
		PendingBatches: iv.pending,
	}, nil
}

func (s *Scheduler) Watermark() Offset {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watermark
}

func (s *Scheduler) HoldProbes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probes
}

func (s *Scheduler) cleanIntervalHeap(iv *interval) {
	for iv.batchHeap.Len() > 0 {
		s.probes++
		top := iv.batchHeap[0]
		b := s.batches[top.id]
		if b != nil && !b.acked {
			return
		}
		heap.Pop(&iv.batchHeap)
	}
}

func (s *Scheduler) refreshWatermark() {
	for s.globalHeap.Len() > 0 {
		s.probes++
		top := s.globalHeap[0]
		iv := s.intervals[top.id]
		if iv != nil && iv.version == top.version && !isComplete(iv) && intervalHold(iv) == top.hold {
			if top.hold > s.watermark {
				s.watermark = top.hold
			}
			return
		}
		heap.Pop(&s.globalHeap)
	}
}

func intervalHold(iv *interval) Offset {
	if iv.batchHeap.Len() == 0 {
		return iv.next
	}
	return min64(iv.next, iv.batchHeap[0].from)
}

func isComplete(iv *interval) bool {
	return iv.next == iv.to && iv.pending == 0
}

func ceilDivBig(value *big.Int, denominator int64) int64 {
	remainder := new(big.Int)
	quotient, _ := new(big.Int).QuoRem(value, big.NewInt(denominator), remainder)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient.Int64()
}

func min64(a, b Offset) Offset {
	if a < b {
		return a
	}
	return b
}
