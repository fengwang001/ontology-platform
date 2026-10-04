// Package bulkhead tracks concurrency permits, the FIFO wait queue and
// the lifecycle of every issued id.
package bulkhead

// Outcome is the lifecycle state of an issued id.
type Outcome int

const (
	Queued Outcome = iota
	InService
	Released
	TimedOut
	Revoked
)

func (o Outcome) String() string {
	switch o {
	case Queued:
		return "排队中"
	case InService:
		return "在役"
	case Released:
		return "已归还"
	case TimedOut:
		return "超时"
	case Revoked:
		return "熔断撤销"
	}
	return "未知"
}

type waiter struct {
	id         int
	enqueuedAt int64
}

type record struct {
	outcome Outcome
	epoch   uint64 // epoch at which the permit became in-service
}

// Bulkhead hands out at most c in-service permits and queues up to q
// waiters. Ids are issued sequentially from 1 and never reused.
type Bulkhead struct {
	permits  int
	queueCap int

	nextID    int
	inService int
	queue     []waiter
	head      int
	rec       map[int]record
	counts    [5]int // per-Outcome tallies
}

// New returns a bulkhead with the given permit count and queue capacity.
func New(permits, queueCap int) *Bulkhead {
	return &Bulkhead{
		permits:  permits,
		queueCap: queueCap,
		rec:      make(map[int]record),
	}
}

// Issued returns the number of ids issued so far.
func (b *Bulkhead) Issued() int { return b.nextID }

// InService returns the number of permits currently in service.
func (b *Bulkhead) InService() int { return b.inService }

// QueueLen returns the number of waiters currently queued.
func (b *Bulkhead) QueueLen() int { return len(b.queue) - b.head }

// Counts returns per-outcome tallies; their sum always equals Issued.
func (b *Bulkhead) Counts() [5]int { return b.counts }

// Known reports whether id was ever issued.
func (b *Bulkhead) Known(id int) bool {
	_, ok := b.rec[id]
	return ok
}

// IsInService reports whether id currently holds a permit.
func (b *Bulkhead) IsInService(id int) bool {
	r, ok := b.rec[id]
	return ok && r.outcome == InService
}

// Status returns the current outcome of id.
func (b *Bulkhead) Status(id int) (Outcome, bool) {
	r, ok := b.rec[id]
	return r.outcome, ok
}

// Grant issues a new in-service permit if one is free.
func (b *Bulkhead) Grant(epoch uint64) (int, bool) {
	if b.inService >= b.permits {
		return 0, false
	}
	b.nextID++
	b.inService++
	b.rec[b.nextID] = record{outcome: InService, epoch: epoch}
	b.counts[InService]++
	return b.nextID, true
}

// Enqueue issues a new queued id if the queue has room.
func (b *Bulkhead) Enqueue(now int64) (int, bool) {
	if b.QueueLen() >= b.queueCap {
		return 0, false
	}
	b.nextID++
	b.queue = append(b.queue, waiter{id: b.nextID, enqueuedAt: now})
	b.rec[b.nextID] = record{outcome: Queued}
	b.counts[Queued]++
	return b.nextID, true
}

// SettleTimeouts dequeues, in enqueue order, every waiter whose
// enqueuedAt+waitMillis is not after now, marking them TimedOut.
func (b *Bulkhead) SettleTimeouts(now, waitMillis int64) {
	for b.head < len(b.queue) && b.queue[b.head].enqueuedAt+waitMillis <= now {
		id := b.queue[b.head].id
		b.head++
		b.rec[id] = record{outcome: TimedOut}
		b.counts[Queued]--
		b.counts[TimedOut]++
	}
	b.compact()
}

// Release returns the permit held by id, reporting its issuance epoch.
// It fails unless id is currently in service.
func (b *Bulkhead) Release(id int) (epoch uint64, ok bool) {
	r, known := b.rec[id]
	if !known || r.outcome != InService {
		return 0, false
	}
	b.rec[id] = record{outcome: Released, epoch: r.epoch}
	b.counts[InService]--
	b.counts[Released]++
	b.inService--
	return r.epoch, true
}

// DrainQueue grants free permits to queued waiters in enqueue order,
// marking them in-service at the given epoch. Returns the granted ids.
func (b *Bulkhead) DrainQueue(epoch uint64) []int {
	var granted []int
	for b.inService < b.permits && b.head < len(b.queue) {
		w := b.queue[b.head]
		b.head++
		b.rec[w.id] = record{outcome: InService, epoch: epoch}
		b.counts[Queued]--
		b.counts[InService]++
		b.inService++
		granted = append(granted, w.id)
	}
	b.compact()
	return granted
}

// RevokeAll dequeues every waiter, marking them Revoked in enqueue order.
func (b *Bulkhead) RevokeAll() []int {
	revoked := make([]int, 0, b.QueueLen())
	for ; b.head < len(b.queue); b.head++ {
		id := b.queue[b.head].id
		b.rec[id] = record{outcome: Revoked}
		b.counts[Queued]--
		b.counts[Revoked]++
		revoked = append(revoked, id)
	}
	b.compact()
	return revoked
}

func (b *Bulkhead) compact() {
	if b.head == len(b.queue) {
		b.queue = b.queue[:0]
		b.head = 0
	}
}
