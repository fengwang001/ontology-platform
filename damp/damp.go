// Package damp implements a per-route route-flap dampening state machine.
//
// All penalties are non-negative integers decaying in fixed steps. Suppression
// and reuse are pure functions of the accepted operation sequence and its
// timestamps, so an identical sequence replays to identical events.
package damp

import (
	"errors"
	"sync"

	"ontology/decay"
	"ontology/reuse"
)

// Sentinel errors. Use errors.Is to distinguish rejection classes; the
// precedence is invalid argument > clock rollback > state errors.
var (
	ErrInvalidArgument = errors.New("damp: invalid argument")
	ErrClockRollback   = errors.New("damp: clock rollback")
	ErrRouteNotFound   = errors.New("damp: route not found")
	ErrPeerNotFound    = errors.New("damp: peer not found")
)

const (
	announcePenalty = 500
	withdrawPenalty = 1000
)

// Event is one reuse event. Usable reports whether the route is advertised
// and unsuppressed right after being released.
type Event struct {
	Peer    int64
	Prefix  int64
	ReuseAt int64
	Usable  bool
}

type route struct {
	key        reuse.Key
	advertised bool
	attr       int64

	hasRecord bool
	p         int64
	last      int64

	suppressed bool
	supSince   int64
	timer      *reuse.Entry
}

// Dampener is safe for concurrent use.
type Dampener struct {
	mu sync.Mutex

	dec    *decay.Decayer
	sched  *reuse.Scheduler
	ps     int64
	pr     int64
	pmax   int64
	tmax   int64
	maxNow int64
	used   bool

	routes map[reuse.Key]*route
}

// New validates all parameters.
func New(delta, num, den, ps, pr, pmax, tmax int64) (*Dampener, error) {
	dec, err := decay.New(delta, num, den, pmax)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	if !(1 <= pr && pr < ps && ps <= pmax && pmax <= 1_000_000_000) {
		return nil, ErrInvalidArgument
	}
	if tmax < 1 || tmax > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	return &Dampener{
		dec:    dec,
		sched:  reuse.New(),
		ps:     ps,
		pr:     pr,
		pmax:   pmax,
		tmax:   tmax,
		routes: make(map[reuse.Key]*route),
	}, nil
}

// MulSteps exposes the underlying decayer multiplication counter for proofs.
// It reads the counter set by the most recent internal arithmetic call and is
// best inspected in single-threaded tests.
func (d *Dampener) MulSteps() int64 { return d.dec.MulSteps() }

// Z returns the zero-decay step bound computed once at construction.
func (d *Dampener) Z() int64 { return d.dec.Z() }

func validKey(peer, prefix int64) bool {
	return 1 <= peer && peer <= 1_000_000 && 1 <= prefix && prefix <= 1_000_000_000
}

func validNow(now int64) bool { return 0 <= now && now <= 1_000_000_000_000 }

// Announce applies an announce carrying a 64-bit attribute digest.
func (d *Dampener) Announce(peer, prefix, attr, now int64) ([]Event, error) {
	key := reuse.Key{Peer: peer, Prefix: prefix}
	if !validKey(peer, prefix) || !validNow(now) {
		return nil, ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.used && now < d.maxNow {
		return nil, ErrClockRollback
	}
	events := d.drainDue(now)
	r := d.routes[key]
	if r == nil {
		r = &route{key: key, advertised: true, attr: attr}
		d.routes[key] = r
	} else if !r.advertised {
		r.advertised = true
		r.attr = attr
	} else if r.attr != attr {
		r.attr = attr
		d.applyPenalty(r, announcePenalty, now)
	}
	d.advance(now)
	return events, nil
}

// Withdraw withdraws one advertised route and penalizes it.
func (d *Dampener) Withdraw(peer, prefix, now int64) ([]Event, error) {
	key := reuse.Key{Peer: peer, Prefix: prefix}
	if !validKey(peer, prefix) || !validNow(now) {
		return nil, ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.used && now < d.maxNow {
		return nil, ErrClockRollback
	}
	r := d.routes[key]
	if r == nil || !r.advertised {
		return nil, ErrRouteNotFound
	}
	events := d.drainDue(now)
	r.advertised = false
	d.applyPenalty(r, withdrawPenalty, now)
	d.advance(now)
	return events, nil
}

// PeerDown marks every advertised route of the peer withdrawn without adding
// penalties; penalty and suppression state are preserved.
func (d *Dampener) PeerDown(peer, now int64) ([]Event, error) {
	if peer < 1 || peer > 1_000_000 || !validNow(now) {
		return nil, ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.used && now < d.maxNow {
		return nil, ErrClockRollback
	}
	exists := false
	for _, r := range d.routes {
		if r.key.Peer == peer && r.advertised {
			exists = true
			break
		}
	}
	if !exists {
		return nil, ErrPeerNotFound
	}
	events := d.drainDue(now)
	for _, r := range d.routes {
		if r.key.Peer == peer && r.advertised {
			r.advertised = false
		}
	}
	d.advance(now)
	return events, nil
}

// Tick only releases due routes; it changes no route penalty state.
func (d *Dampener) Tick(now int64) ([]Event, error) {
	if !validNow(now) {
		return nil, ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.used && now < d.maxNow {
		return nil, ErrClockRollback
	}
	events := d.drainDue(now)
	d.advance(now)
	return events, nil
}

// Penalty is a read-only settled view at t: it neither advances the clock nor
// persists any state. Missing records report zero and unsuppressed.
func (d *Dampener) Penalty(peer, prefix, t int64) (p int64, suppressed bool, err error) {
	if !validKey(peer, prefix) || !validNow(t) {
		return 0, false, ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.used && t < d.maxNow {
		return 0, false, ErrClockRollback
	}
	r := d.routes[reuse.Key{Peer: peer, Prefix: prefix}]
	if r == nil || !r.hasRecord {
		return 0, false, nil
	}
	np, _ := d.dec.Settle(r.p, r.last, t)
	sup := r.suppressed
	if sup && r.timer != nil && r.timer.ReuseAt <= t {
		sup = false
	}
	return np, sup, nil
}

// drainDue releases every route whose reuse instant has arrived, before the
// current operation executes. Tmax release does not settle p.
func (d *Dampener) drainDue(now int64) []Event {
	due := d.sched.PopDue(now)
	events := make([]Event, 0, len(due))
	for _, e := range due {
		r := d.routes[e.Key]
		r.suppressed = false
		r.timer = nil
		events = append(events, Event{
			Peer:    e.Peer,
			Prefix:  e.Prefix,
			ReuseAt: e.ReuseAt,
			Usable:  r.advertised,
		})
	}
	return events
}

// applyPenalty settles the route to now, adds the penalty (clamped to Pmax),
// and (re)evaluates suppression using the freshly settled phase.
func (d *Dampener) applyPenalty(r *route, amount, now int64) {
	if !r.hasRecord {
		r.hasRecord = true
		r.p = 0
		r.last = now
	}
	r.p, r.last = d.dec.Settle(r.p, r.last, now)
	r.p += amount
	if r.p > d.pmax {
		r.p = d.pmax
	}
	if !r.suppressed && r.p >= d.ps {
		r.suppressed = true
		r.supSince = now
		r.timer = d.schedule(r)
	} else if r.suppressed {
		// supSince is unchanged; reuse instant is recomputed from new state.
		d.sched.Remove(r.timer)
		r.timer = d.schedule(r)
	}
}

func (d *Dampener) schedule(r *route) *reuse.Entry {
	j := d.dec.StepsToBelow(r.p, d.pr)
	reuseAt := r.last + j*d.dec.Delta()
	if tmaxAt := r.supSince + d.tmax; tmaxAt < reuseAt {
		reuseAt = tmaxAt
	}
	e := &reuse.Entry{Key: r.key, ReuseAt: reuseAt}
	d.sched.Add(e)
	return e
}

func (d *Dampener) advance(now int64) {
	d.used = true
	if now > d.maxNow {
		d.maxNow = now
	}
}
