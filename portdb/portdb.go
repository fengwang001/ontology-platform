// Package portdb stores porting orders, effective ports and disconnect freezes.
//
// Each number owns an append-only history of effective events sorted by time.
// A port event carries the recipient operator; a disconnect event carries
// recipient 0 and shadows every earlier port while its freeze window
// [at, at+Q) lasts, and permanently after the freeze as well (a disconnect
// clears port records; only a later port can restore a non-home server).
// Orders cancelled before their at never enter the history, so the serving
// operator at any time is a pure function of the stored history — no
// background job is needed to apply ports or thaw freezes.
package portdb

import (
	"sync"
	"sync/atomic"

	"ontology/numplan"
)

const maxTime = int64(1_000_000_000_000)

var (
	ErrInvalidArgument   = numplan.ErrInvalidArgument
	ErrClockRewound      = numplan.ErrClockRewound
	ErrNumberUnallocated = numplan.ErrNumberUnallocated
	ErrFrozen            = numplan.ErrFrozen
	ErrPendingExists     = numplan.ErrPendingExists
	ErrDonorMismatch     = numplan.ErrDonorMismatch
	ErrSameOperator      = numplan.ErrSameOperator
	ErrLeadTime          = numplan.ErrLeadTime
	ErrOrderNotFound     = numplan.ErrOrderNotFound
	ErrAlreadyEffective  = numplan.ErrAlreadyEffective
)

// event is an effective history entry. recipient 0 marks a disconnect.
// Events are sorted by (at, kind): a disconnect sorts after a port at the
// same at so that disconnects-at-equal-time win, matching serial execution.
type event struct {
	at        int64
	recipient int64
}

type order struct {
	id        int64
	number    string
	donor     int64
	recipient int64
	at        int64
	committed bool // moved into events once its at was reached
	cancelled bool
}

type rec struct {
	events  []event // effective events, sorted by (at, disconnect-last)
	pending *order  // the single not-yet-effective order, or nil
}

// DB is the porting database.
type DB struct {
	mu      sync.RWMutex
	plan    *numplan.Plan
	recs    map[string]*rec
	orders  map[int64]*order
	lmin    int64
	q       int64
	nextID  int64
	maxNow  int64
	blocks  atomic.Int64 // block records probed since last Probes
	history atomic.Int64 // distinct history records probed since last Probes
}

// New creates a DB with minimum lead time Lmin and freeze duration Q.
func New(Lmin, Q int64) *DB {
	return &DB{recs: make(map[string]*rec), orders: make(map[int64]*order), lmin: Lmin, q: Q}
}

// SetPlan attaches the number plan used for home-operator lookups.
func (d *DB) SetPlan(p *numplan.Plan) {
	d.mu.Lock()
	d.plan = p
	d.mu.Unlock()
}

// RequestPort registers a porting order and returns its contiguous order id;
// rejected requests do not consume an id. The eight rejection reasons are
// checked in the mandated precedence and never mutate state or the clock.
func (d *DB) RequestPort(number string, donor, recipient, at, now int64) (int64, error) {
	if !numplan.ValidNumber(number) ||
		!numplan.ValidOperator(donor) || !numplan.ValidOperator(recipient) ||
		at < 0 || at > maxTime || now < 0 || now > maxTime {
		return 0, ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if now < d.maxNow {
		return 0, ErrClockRewound
	}
	server, _, frozen, err := d.evalLocked(number, now, false)
	if err != nil {
		return 0, err
	}
	if frozen {
		return 0, ErrFrozen
	}
	rc := d.recs[number]
	if rc != nil && rc.pending != nil && rc.pending.at > now {
		return 0, ErrPendingExists
	}
	if donor != server {
		return 0, ErrDonorMismatch
	}
	if recipient == donor {
		return 0, ErrSameOperator
	}
	if at-now < d.lmin {
		return 0, ErrLeadTime
	}
	if rc == nil {
		rc = &rec{}
		d.recs[number] = rc
	}
	// A pending order whose at has been reached has already taken effect.
	rc.pending = nil
	d.nextID++
	id := d.nextID
	o := &order{id: id, number: number, donor: donor, recipient: recipient, at: at}
	d.maxNow = now
	if at == now {
		d.insertLocked(rc, event{at: at, recipient: recipient})
		o.committed = true
	} else {
		rc.pending = o
	}
	d.orders[id] = o
	return id, nil
}

// Cancel cancels a not-yet-effective order; the order never takes effect.
func (d *DB) Cancel(orderID, now int64) error {
	if orderID < 1 || now < 0 || now > maxTime {
		return ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if now < d.maxNow {
		return ErrClockRewound
	}
	o := d.orders[orderID]
	if o == nil || o.cancelled {
		return ErrOrderNotFound
	}
	effective := o.committed || now >= o.at
	if effective {
		return ErrAlreadyEffective
	}
	if rc := d.recs[o.number]; rc != nil && rc.pending != nil && rc.pending.id == o.id {
		rc.pending = nil
	}
	o.cancelled = true
	d.maxNow = now
	return nil
}

// Disconnect clears port records, auto-cancels any pending order and freezes
// the number over [now, now+Q); after thaw it returns to its home operator.
func (d *DB) Disconnect(number string, now int64) error {
	if !numplan.ValidNumber(number) || now < 0 || now > maxTime {
		return ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if now < d.maxNow {
		return ErrClockRewound
	}
	_, _, frozen, err := d.evalLocked(number, now, false)
	if err != nil {
		return err
	}
	if frozen {
		return ErrFrozen
	}
	rc := d.recs[number]
	if rc == nil {
		rc = &rec{}
		d.recs[number] = rc
	}
	if rc.pending != nil {
		rc.pending.cancelled = true
		rc.pending = nil
	}
	d.insertLocked(rc, event{at: now, recipient: 0})
	d.maxNow = now
	return nil
}

// LookupAt returns the serving and home operators at time t.
func (d *DB) LookupAt(number string, t int64) (server, home int64, frozen bool, err error) {
	if !numplan.ValidNumber(number) || t < 0 || t > maxTime {
		return 0, 0, false, ErrInvalidArgument
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.evalLocked(number, t, true)
}

// evalLocked evaluates server/home/frozen at t. With count=true it
// accumulates into the probe counters. The number of distinct history
// records touched is at most floor(log2(m)) + 2 for m stored events.
func (d *DB) evalLocked(number string, t int64, count bool) (server, home int64, frozen bool, err error) {
	var blockProbes int64
	if count {
		home = d.plan.OwnerAtProbed(number, t, &blockProbes)
	} else {
		var ok bool
		home, ok = d.plan.OwnerAt(number, t)
		if !ok {
			return 0, 0, false, ErrNumberUnallocated
		}
	}
	if home == 0 {
		return 0, 0, false, ErrNumberUnallocated
	}
	rc := d.recs[number]
	if rc == nil {
		if count {
			d.blocks.Add(blockProbes)
		}
		return home, home, false, nil
	}
	// Lazily commit an order whose at has been reached. This is the only
	// mutation needed for a port to "take effect": a pure function of time,
	// performed on first observation and therefore race-free under the lock.
	if rc.pending != nil && rc.pending.at <= t {
		if !rc.pending.committed && !rc.pending.cancelled {
			d.insertLocked(rc, event{at: rc.pending.at, recipient: rc.pending.recipient})
			rc.pending.committed = true
		}
		rc.pending = nil
	}
	events := rc.events
	idx, probed := bisectRight(events, t)
	if idx >= 0 && events[idx].recipient == 0 {
		// Latest effective event is a disconnect: frozen inside [at, at+Q),
		// otherwise earlier ports are cleared and service returns to home.
		if t < events[idx].at+d.q {
			frozen = true
		}
		if !frozen {
			server = home
		}
	} else if idx >= 0 {
		server = events[idx].recipient
	} else {
		server = home
	}
	if count {
		d.blocks.Add(blockProbes)
		d.history.Add(int64(probed))
	}
	if frozen {
		return 0, home, true, nil
	}
	return server, home, false, nil
}

// bisectRight returns the index of the rightmost event with at <= t (-1 when
// none) together with the number of distinct array indices examined by the
// binary search. It never inspects more than floor(log2(m)) + 2 indices.
func bisectRight(events []event, t int64) (idx, probed int) {
	seen := make(map[int]bool, len(events))
	lo, hi := 0, len(events)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		seen[mid] = true
		if events[mid].at <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1, len(seen)
}

// insertLocked keeps events sorted by (at, port-before-disconnect). Equal
// (at, kind) pairs cannot repeat: a rejected port never inserts and a
// disconnect while frozen is rejected upstream.
func (d *DB) insertLocked(rc *rec, ev event) {
	lo, hi := 0, len(rc.events)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		cur := rc.events[mid]
		if cur.at < ev.at || (cur.at == ev.at && cur.recipient != 0 && ev.recipient == 0) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	rc.events = append(rc.events, event{})
	copy(rc.events[lo+1:], rc.events[lo:])
	rc.events[lo] = ev
}

// MaxNow reports the largest accepted timestamp.
func (d *DB) MaxNow() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.maxNow
}

// Probes returns the records examined since the previous Probes call and
// resets both counters.
func (d *DB) Probes() (blockProbes, historyProbes int64) {
	return d.blocks.Swap(0), d.history.Swap(0)
}
