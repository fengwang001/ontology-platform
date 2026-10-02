// Package schedqueue implements a deterministic scheduling queue with an
// active queue, a backoff queue, an unschedulable queue and an in-flight
// set, with event-mask-filtered moves between them.
package schedqueue

import (
	"fmt"
	"sync"
)

// Outcome is the result reported by Done for an in-flight pod.
type Outcome int

const (
	// Scheduled means the pod was scheduled and is removed from the queue.
	Scheduled Outcome = iota
	// Failed means scheduling failed; the pod is backed off or parked.
	Failed
)

// RejectReason identifies why an operation was rejected.
type RejectReason int

const (
	// RejectInvalidConfig: constructor parameters out of range.
	RejectInvalidConfig RejectReason = iota
	// RejectInvalidArgument: empty ID, negative now, bad outcome/fb/ev.
	RejectInvalidArgument
	// RejectClockRegression: now is below the max now of accepted ops.
	RejectClockRegression
	// RejectDuplicateID: Add with an ID that already exists.
	RejectDuplicateID
	// RejectPodNotFound: Done/Remove for an unknown pod.
	RejectPodNotFound
	// RejectPodNotInFlight: Done for a pod that is not in flight.
	RejectPodNotInFlight
)

// Error is the rejection error returned by all fallible operations.
type Error struct {
	Reason RejectReason
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func reject(reason RejectReason, format string, args ...any) *Error {
	return &Error{Reason: reason, Msg: fmt.Sprintf(format, args...)}
}

const (
	maxBound = int64(1_000_000_000_000) // 1e12
	maxMask  = 255
)

// state is the location of a live pod.
type state int

const (
	stateActive state = iota
	stateBackoff
	stateUnschedulable
	stateInFlight
)

// pod is the internal mutable record for a live pod.
type pod struct {
	id     string
	prio   int
	t0     int64
	att    int
	st     state
	exp    int64 // backoff expiry, valid in backoff/unschedulable
	parked int64 // time parked in unschedulable
	fb     int   // last failure mask, valid in backoff/unschedulable
	seq    int64 // event seq recorded at Pop, valid in flight
	index  int   // index within the active or backoff heap
}

// Pod is the snapshot returned by Pop.
type Pod struct {
	ID   string
	Prio int
	T0   int64
	Att  int
}

// Queue is a goroutine-safe scheduling queue.
type Queue struct {
	mu       sync.Mutex
	base     int64
	cap      int64
	ttl      int64
	maxNow   int64
	seq      int64
	events   []int // events[i] is the mask of event seq i+1
	pods     map[string]*pod
	active   activeHeap
	backoff  backoffHeap
	unsched  map[string]*pod
	inFlight map[string]*pod
}

// New builds a Queue. B is the backoff base, M the backoff cap and L the
// unschedulable TTL, all in milliseconds with 1 <= B <= M <= 1e12 and
// 1 <= L <= 1e12.
func New(B, M, L int64) (*Queue, error) {
	if B < 1 || M < B || M > maxBound || L < 1 || L > maxBound {
		return nil, reject(RejectInvalidConfig,
			"invalid config: B=%d M=%d L=%d, need 1<=B<=M<=1e12 and 1<=L<=1e12", B, M, L)
	}
	q := &Queue{
		base:     B,
		cap:      M,
		ttl:      L,
		pods:     make(map[string]*pod),
		unsched:  make(map[string]*pod),
		inFlight: make(map[string]*pod),
	}
	return q, nil
}

// Add inserts a new pod into the active queue.
func (q *Queue) Add(id string, prio int, now int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if id == "" || now < 0 {
		return reject(RejectInvalidArgument, "invalid argument: id=%q now=%d", id, now)
	}
	if err := q.checkClock(now); err != nil {
		return err
	}
	if _, ok := q.pods[id]; ok {
		return reject(RejectDuplicateID, "duplicate id: %q", id)
	}
	p := &pod{id: id, prio: prio, t0: now}
	q.pods[id] = p
	q.pushActive(p)
	return nil
}

// Pop advances expired pods, then pops the highest-priority active pod and
// marks it in flight. ok is false when the active queue is empty.
func (q *Queue) Pop(now int64) (p Pod, ok bool, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if now < 0 {
		return Pod{}, false, reject(RejectInvalidArgument, "invalid argument: now=%d", now)
	}
	if err := q.checkClock(now); err != nil {
		return Pod{}, false, err
	}
	q.advanceLocked(now)
	if len(q.active) == 0 {
		return Pod{}, false, nil
	}
	top := q.active[0]
	heapRemoveActive(q, top)
	top.st = stateInFlight
	top.att++
	top.seq = q.seq
	q.inFlight[top.id] = top
	return Pod{ID: top.id, Prio: top.prio, T0: top.t0, Att: top.att}, true, nil
}

// Done resolves an in-flight pod as Scheduled or Failed with mask fb.
func (q *Queue) Done(id string, outcome Outcome, fb int, now int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if id == "" || now < 0 || (outcome != Scheduled && outcome != Failed) || fb < 0 || fb > maxMask {
		return reject(RejectInvalidArgument,
			"invalid argument: id=%q outcome=%d fb=%d now=%d", id, outcome, fb, now)
	}
	if err := q.checkClock(now); err != nil {
		return err
	}
	p, ok := q.pods[id]
	if !ok {
		return reject(RejectPodNotFound, "pod not found: %q", id)
	}
	if p.st != stateInFlight {
		return reject(RejectPodNotInFlight, "pod not in flight: %q", id)
	}
	delete(q.inFlight, id)
	if outcome == Scheduled {
		delete(q.pods, id)
		return nil
	}
	p.fb = fb
	p.exp = addSaturated(now, backoffDuration(q.base, q.cap, p.att))
	if q.relatedEventAfter(p.seq, fb) {
		q.pushBackoff(p)
	} else {
		q.pushUnsched(p, now)
	}
	return nil
}

// Event records event mask ev and moves related unschedulable pods.
func (q *Queue) Event(ev int, now int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if now < 0 || ev < 1 || ev > maxMask {
		return reject(RejectInvalidArgument, "invalid argument: ev=%d now=%d", ev, now)
	}
	if err := q.checkClock(now); err != nil {
		return err
	}
	q.seq++
	q.events = append(q.events, ev)
	for _, p := range q.unsched {
		if related(p.fb, ev) {
			q.moveParked(p, now)
		}
	}
	return nil
}

// Advance moves backoff-expired and TTL-expired pods without an event.
func (q *Queue) Advance(now int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if now < 0 {
		return reject(RejectInvalidArgument, "invalid argument: now=%d", now)
	}
	if err := q.checkClock(now); err != nil {
		return err
	}
	q.advanceLocked(now)
	return nil
}

// Remove deletes a pod from any location, including in flight.
func (q *Queue) Remove(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if id == "" {
		return reject(RejectInvalidArgument, "invalid argument: empty id")
	}
	p, ok := q.pods[id]
	if !ok {
		return reject(RejectPodNotFound, "pod not found: %q", id)
	}
	q.removeFromLocation(p)
	delete(q.pods, id)
	return nil
}

// Sizes returns member counts of active, backoff, unschedulable, in-flight.
func (q *Queue) Sizes() (active, backoff, unschedulable, inFlight int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.active), len(q.backoff), len(q.unsched), len(q.inFlight)
}

// checkClock rejects clock regression and records the new max now.
// Callers must hold q.mu and have validated arguments already.
func (q *Queue) checkClock(now int64) error {
	if now < q.maxNow {
		return reject(RejectClockRegression, "clock regression: now=%d < max=%d", now, q.maxNow)
	}
	q.maxNow = now
	return nil
}

// related reports whether failure mask fb and event mask ev are related.
func related(fb, ev int) bool {
	return fb == 0 || fb&ev != 0
}

// relatedEventAfter reports whether any event with seq > after is related
// to failure mask fb.
func (q *Queue) relatedEventAfter(after int64, fb int) bool {
	if int64(len(q.events)) <= after {
		return false
	}
	if fb == 0 {
		return true
	}
	for _, ev := range q.events[after:] {
		if fb&ev != 0 {
			return true
		}
	}
	return false
}

// backoffDuration returns min(B*2^(att-1), M) without overflow.
func backoffDuration(B, M int64, att int) int64 {
	d := B
	for i := 1; i < att; i++ {
		if d >= M || 2*d >= M {
			return M
		}
		d *= 2
	}
	return d
}

// addSaturated adds d to t, saturating at the maximum int64.
func addSaturated(t, d int64) int64 {
	if t > int64(^uint64(0)>>1)-d {
		return int64(^uint64(0) >> 1)
	}
	return t + d
}

// advanceLocked moves backoff-expired pods to active and TTL-expired
// unschedulable pods to backoff or active. Callers must hold q.mu.
func (q *Queue) advanceLocked(now int64) {
	for len(q.backoff) > 0 && q.backoff[0].exp <= now {
		p := q.backoff[0]
		heapRemoveBackoff(q, p)
		q.pushActive(p)
	}
	for _, p := range q.unsched {
		if addSaturated(p.parked, q.ttl) <= now {
			q.moveParked(p, now)
		}
	}
}

// moveParked moves an unschedulable pod to backoff when now < exp,
// otherwise to active. Callers must hold q.mu.
func (q *Queue) moveParked(p *pod, now int64) {
	delete(q.unsched, p.id)
	if now < p.exp {
		q.pushBackoff(p)
	} else {
		q.pushActive(p)
	}
}
