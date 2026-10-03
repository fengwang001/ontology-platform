// Package spotmarket implements a spot capacity market with a uniform
// clearing price, interruption-free hours and hour-boundary billing rules.
//
// All non-terminated requests are ranked by (bid descending, submission
// sequence ascending). The top K requests (K = current capacity) form the
// running set; the rest wait. The clearing price is the bid of the
// (K+1)-th ranked request, or the floor price when fewer than K+1 requests
// exist. A running segment [s, e) ended by the user is billed for
// ceil((e-s)/3600) hours; a segment ended by a market interruption is
// billed for floor((e-s)/3600) hours. The k-th hour (k from 0) is priced
// at price(s+3600*k), where price(x) is the last recorded clearing price
// at a time not greater than x.
package spotmarket

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// HourSeconds is the fixed billing hour length in seconds.
const HourSeconds int64 = 3600

const (
	minCapacity int64 = 1
	maxCapacity int64 = 1000
	minPrice    int64 = 1
	maxPrice    int64 = 1_000_000_000
	minID       int64 = 0
	maxID       int64 = 1_000_000_000
	minTime     int64 = 0
	maxTime     int64 = 1_000_000_000_000_000
)

// Rejection reasons. Each operation reports only the first matching reason
// in its documented order.
var (
	ErrInvalidConfig     = errors.New("spotmarket: invalid configuration")
	ErrInvalidParam      = errors.New("spotmarket: invalid parameter")
	ErrClockRegression   = errors.New("spotmarket: clock regression")
	ErrDuplicateID       = errors.New("spotmarket: duplicate request id")
	ErrNotFound          = errors.New("spotmarket: request does not exist")
	ErrAlreadyTerminated = errors.New("spotmarket: request already terminated")
)

// EndReason tells how a running segment ended.
type EndReason int

const (
	// UserTerminated: the user terminated the request; billed with ceil hours.
	UserTerminated EndReason = iota
	// MarketInterrupted: the request fell out of the top K; billed with floor hours.
	MarketInterrupted
)

func (r EndReason) String() string {
	if r == UserTerminated {
		return "user-terminated"
	}
	return "market-interrupted"
}

// EventType distinguishes segment end events from segment start events.
type EventType int

const (
	EventEnd EventType = iota
	EventStart
)

// Event is produced by Request, Terminate and SetCapacity. End events come
// first, then start events, each group sorted by request id ascending.
type Event struct {
	Type   EventType
	ID     int64
	Reason EndReason // End events only
	Fee    *big.Int  // End events: fee of the ended segment
	Start  int64     // End events: segment start; Start events: start time
	End    int64     // End events only: segment end
}

func (e Event) String() string {
	if e.Type == EventStart {
		return fmt.Sprintf("Start#%d@%d", e.ID, e.Start)
	}
	return fmt.Sprintf("End#%d %s fee=%s [%d,%d)", e.ID, e.Reason, e.Fee, e.Start, e.End)
}

type pricePoint struct {
	t     int64
	price int64
}

type request struct {
	id         int64
	bid        int64
	seq        int64
	terminated bool
	running    bool
	segStart   int64
	bill       *big.Int
}

// Market is a spot capacity market. All methods are safe for concurrent
// use; the result is equivalent to some serial order of the calls.
type Market struct {
	mu       sync.Mutex
	capacity int64
	floor    int64
	maxT     int64
	nextSeq  int64
	reqs     map[int64]*request
	history  []pricePoint
}

// New creates a market with the given initial capacity (1..1000) and floor
// price (1..1e9). Out-of-range configuration is rejected as a whole.
func New(capacity, floorPrice int64) (*Market, error) {
	if capacity < minCapacity || capacity > maxCapacity ||
		floorPrice < minPrice || floorPrice > maxPrice {
		return nil, ErrInvalidConfig
	}
	m := &Market{
		capacity: capacity,
		floor:    floorPrice,
		reqs:     make(map[int64]*request),
	}
	m.history = append(m.history, pricePoint{t: 0, price: floorPrice})
	return m, nil
}

// priceAt returns the last recorded price at a time not greater than x.
// Caller must hold the lock.
func (m *Market) priceAt(x int64) int64 {
	i := sort.Search(len(m.history), func(i int) bool { return m.history[i].t > x }) - 1
	return m.history[i].price
}

// ceilDiv returns ceil(n/d) for d > 0; n may be negative.
func ceilDiv(n, d int64) int64 {
	if n >= 0 {
		return (n + d - 1) / d
	}
	return -((-n) / d)
}

// segmentFee settles segment [s, e). User-terminated segments charge
// ceil((e-s)/3600) hours, market-interrupted ones floor((e-s)/3600) hours;
// the k-th hour is priced at price(s+3600*k). Caller must hold the lock.
func (m *Market) segmentFee(s, e int64, user bool) *big.Int {
	d := e - s
	var hours int64
	if user {
		hours = (d + HourSeconds - 1) / HourSeconds
	} else {
		hours = d / HourSeconds
	}
	total := new(big.Int)
	if hours <= 0 {
		return total
	}
	// Hour starts are s+3600*k for k in [0, hours). Walk the price history
	// and count how many hour starts fall into each constant-price interval
	// [hist[i].t, hist[i+1].t).
	lastStart := s + hours*HourSeconds // exclusive bound of hour starts
	for i := 0; i < len(m.history); i++ {
		a := m.history[i].t
		b := lastStart
		if i+1 < len(m.history) {
			b = m.history[i+1].t
		}
		if b > lastStart {
			b = lastStart
		}
		lo := ceilDiv(a-s, HourSeconds)
		hi := ceilDiv(b-s, HourSeconds)
		if lo < 0 {
			lo = 0
		}
		if hi > hours {
			hi = hours
		}
		if lo < hi {
			total.Add(total, new(big.Int).Mul(
				big.NewInt(m.history[i].price), big.NewInt(hi-lo)))
		}
	}
	return total
}

// endSegment closes the running segment of r at time e and returns the end
// event. Caller must hold the lock.
func (m *Market) endSegment(r *request, e int64, reason EndReason) Event {
	fee := m.segmentFee(r.segStart, e, reason == UserTerminated)
	r.bill.Add(r.bill, fee)
	r.running = false
	return Event{
		Type:   EventEnd,
		ID:     r.id,
		Reason: reason,
		Fee:    fee,
		Start:  r.segStart,
		End:    e,
	}
}

// rankedActive returns all non-terminated requests sorted by
// (bid descending, seq ascending). Caller must hold the lock.
func (m *Market) rankedActive() []*request {
	active := make([]*request, 0, len(m.reqs))
	for _, r := range m.reqs {
		if !r.terminated {
			active = append(active, r)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].bid != active[j].bid {
			return active[i].bid > active[j].bid
		}
		return active[i].seq < active[j].seq
	})
	return active
}

// recompute re-evaluates the ranking at time t after an operation applied
// itself. It ends segments of requests that fell out of the top K (market
// interruption), starts segments of requests that entered the top K, and
// records the new clearing price if it changed. Returned end events and
// start events are each sorted by id ascending. Caller must hold the lock.
func (m *Market) recompute(t int64) (ends, starts []Event) {
	active := m.rankedActive()
	k := m.capacity
	if k > int64(len(active)) {
		k = int64(len(active))
	}
	newRun := make(map[int64]bool, k)
	for i := int64(0); i < k; i++ {
		newRun[active[i].id] = true
	}
	for _, r := range m.reqs {
		if r.running && !newRun[r.id] {
			ends = append(ends, m.endSegment(r, t, MarketInterrupted))
		}
	}
	for i := int64(0); i < k; i++ {
		r := active[i]
		if !r.running {
			r.running = true
			r.segStart = t
			starts = append(starts, Event{
				Type:  EventStart,
				ID:    r.id,
				Fee:   new(big.Int),
				Start: t,
			})
		}
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i].ID < ends[j].ID })
	sort.Slice(starts, func(i, j int) bool { return starts[i].ID < starts[j].ID })

	clearing := m.floor
	if int64(len(active)) > m.capacity {
		clearing = active[m.capacity].bid
	}
	last := &m.history[len(m.history)-1]
	if last.price != clearing {
		if last.t == t {
			// Multiple records at the same t: the last one wins.
			last.price = clearing
		} else {
			m.history = append(m.history, pricePoint{t: t, price: clearing})
		}
	}
	return ends, starts
}

func joinEvents(ends, starts []Event) []Event {
	events := make([]Event, 0, len(ends)+len(starts))
	events = append(events, ends...)
	events = append(events, starts...)
	return events
}

// Request submits a persistent request. Rejection reasons, first match
// only: invalid parameter, clock regression, duplicate id (including
// terminated ids).
func (m *Market) Request(id, bid, t int64) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < minID || id > maxID || bid < m.floor || bid > maxPrice ||
		t < minTime || t > maxTime {
		return nil, ErrInvalidParam
	}
	if t < m.maxT {
		return nil, ErrClockRegression
	}
	if _, ok := m.reqs[id]; ok {
		return nil, ErrDuplicateID
	}
	r := &request{id: id, bid: bid, seq: m.nextSeq, bill: new(big.Int)}
	m.nextSeq++
	m.reqs[id] = r
	ends, starts := m.recompute(t)
	m.maxT = t
	return joinEvents(ends, starts), nil
}

// Terminate terminates a request on behalf of the user. Rejection reasons,
// first match only: invalid parameter, clock regression, not found,
// already terminated.
func (m *Market) Terminate(id, t int64) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id < minID || id > maxID || t < minTime || t > maxTime {
		return nil, ErrInvalidParam
	}
	if t < m.maxT {
		return nil, ErrClockRegression
	}
	r, ok := m.reqs[id]
	if !ok {
		return nil, ErrNotFound
	}
	if r.terminated {
		return nil, ErrAlreadyTerminated
	}
	r.terminated = true
	var ends []Event
	if r.running {
		ends = append(ends, m.endSegment(r, t, UserTerminated))
	}
	ends2, starts := m.recompute(t)
	ends = append(ends, ends2...)
	sort.Slice(ends, func(i, j int) bool { return ends[i].ID < ends[j].ID })
	m.maxT = t
	return joinEvents(ends, starts), nil
}

// SetCapacity changes the capacity to k2 (1..1000). Rejection reasons,
// first match only: invalid parameter, clock regression.
func (m *Market) SetCapacity(k2, t int64) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if k2 < minCapacity || k2 > maxCapacity || t < minTime || t > maxTime {
		return nil, ErrInvalidParam
	}
	if t < m.maxT {
		return nil, ErrClockRegression
	}
	m.capacity = k2
	ends, starts := m.recompute(t)
	m.maxT = t
	return joinEvents(ends, starts), nil
}

// Bill returns the sum of the fees of all ended running segments of the
// request. Unknown or waiting-only requests have a zero bill.
func (m *Market) Bill(id int64) *big.Int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.reqs[id]; ok {
		return new(big.Int).Set(r.bill)
	}
	return new(big.Int)
}

// PriceAt returns the clearing price in effect at time x according to the
// recorded price history.
func (m *Market) PriceAt(x int64) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.priceAt(x)
}

// RunningIDs returns the ids of the currently running requests, sorted
// ascending.
func (m *Market) RunningIDs() []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int64, 0, len(m.reqs))
	for _, r := range m.reqs {
		if r.running {
			ids = append(ids, r.id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
