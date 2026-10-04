// Package route answers call routing sequences based on the caller's query
// method. It is a thin facade over numplan (home operator) and portdb
// (serving operator), applying the ACQ/OR path rules and the global monotone
// clock shared by every accepted operation.
package route

import (
	"sync"

	"ontology/numplan"
	"ontology/portdb"
)

const maxTime = int64(1_000_000_000_000)

var (
	ErrInvalidArgument   = numplan.ErrInvalidArgument
	ErrClockRewound      = numplan.ErrClockRewound
	ErrBlockExists       = numplan.ErrBlockExists
	ErrNumberUnallocated = numplan.ErrNumberUnallocated
	ErrFrozen            = numplan.ErrFrozen
	ErrPendingExists     = numplan.ErrPendingExists
	ErrDonorMismatch     = numplan.ErrDonorMismatch
	ErrSameOperator      = numplan.ErrSameOperator
	ErrLeadTime          = numplan.ErrLeadTime
	ErrOrderNotFound     = numplan.ErrOrderNotFound
	ErrAlreadyEffective  = numplan.ErrAlreadyEffective
)

// Method selects how the caller queries the database.
type Method string

const (
	ACQ Method = "ACQ"
	OR  Method = "OR"
)

// Result is a routing answer. Path is never nil.
type Result struct {
	Home   int64
	Server int64
	Ported bool
	Path   []int64
	Frozen bool
}

// Router is the facade over numplan and portdb.
type Router struct {
	mu     sync.Mutex
	plan   *numplan.Plan
	db     *portdb.DB
	maxNow int64
}

// New creates a facade with minimum lead time Lmin and freeze duration Q.
func New(Lmin, Q int64) *Router {
	if Lmin < 0 || Lmin > 1_000_000_000 || Q < 0 || Q > 1_000_000_000 {
		return nil
	}
	plan := numplan.New()
	db := portdb.New(Lmin, Q)
	db.SetPlan(plan)
	return &Router{plan: plan, db: db}
}

// AssignBlock registers a number block effective from now.
func (r *Router) AssignBlock(prefix string, length int, op, now int64) error {
	if !numplan.ValidOperator(op) {
		return ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockRewound
	}
	if err := r.plan.AssignBlock(prefix, length, op, now); err != nil {
		return err
	}
	r.maxNow = now
	return nil
}

// RequestPort registers a porting order and returns its contiguous order id.
func (r *Router) RequestPort(number string, donor, recipient, at, now int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return 0, ErrClockRewound
	}
	id, err := r.db.RequestPort(number, donor, recipient, at, now)
	if err != nil {
		return 0, err
	}
	r.maxNow = now
	return id, nil
}

// Cancel cancels a not-yet-effective order.
func (r *Router) Cancel(orderID, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockRewound
	}
	if err := r.db.Cancel(orderID, now); err != nil {
		return err
	}
	r.maxNow = now
	return nil
}

// Disconnect clears ports and freezes the number.
func (r *Router) Disconnect(number string, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return ErrClockRewound
	}
	if err := r.db.Disconnect(number, now); err != nil {
		return err
	}
	r.maxNow = now
	return nil
}

// Query answers at now and advances the accepted clock.
func (r *Router) Query(number string, orig int64, method Method, now int64) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now < r.maxNow {
		return Result{}, ErrClockRewound
	}
	res, err := r.queryLocked(number, orig, method, now)
	if err != nil {
		return Result{}, err
	}
	// An accepted Query advances the clock exactly like any other operation.
	if now > r.maxNow {
		r.maxNow = now
	}
	return res, nil
}

// QueryAt answers a historical question. t may not exceed the largest accepted
// now; it is read-only and never advances the clock.
func (r *Router) QueryAt(number string, orig int64, method Method, t int64) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t > r.maxNow {
		return Result{}, ErrInvalidArgument
	}
	return r.queryLocked(number, orig, method, t)
}

func (r *Router) queryLocked(number string, orig int64, method Method, t int64) (Result, error) {
	if !numplan.ValidOperator(orig) || (method != ACQ && method != OR) ||
		t < 0 || t > maxTime {
		return Result{}, ErrInvalidArgument
	}
	server, home, frozen, err := r.db.LookupAt(number, t)
	if err != nil {
		return Result{}, err
	}
	res := Result{Home: home, Server: server, Frozen: frozen, Path: []int64{}}
	if frozen {
		return res, ErrFrozen
	}
	res.Ported = server != home
	switch {
	case server == orig:
		// On-net call: empty sequence.
	case method == ACQ:
		res.Path = []int64{server}
	case home == server || home == orig:
		res.Path = []int64{server}
	default:
		res.Path = []int64{home, server}
	}
	return res, nil
}

// MaxNow reports the largest accepted timestamp.
func (r *Router) MaxNow() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxNow
}

// Plan exposes the underlying number plan.
func (r *Router) Plan() *numplan.Plan { return r.plan }

// DB exposes the underlying porting database.
func (r *Router) DB() *portdb.DB { return r.db }

// Probes returns the records examined since the previous Probes call and
// resets the counters (block records and per-number history records).
func (r *Router) Probes() (blockProbes, historyProbes int64) {
	return r.db.Probes()
}
