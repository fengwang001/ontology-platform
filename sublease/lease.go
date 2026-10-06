// Package sublease implements a lease subletting chain and liability
// propagation service: multi-level subleases under a master lease,
// landlord consent granting/revocation, rent and arrears accounting
// with recourse rights, and cascading termination with survival rules.
//
// All mutating operations are serialized by a single mutex, so any
// concurrent call pattern is equivalent to some serial order. Every
// operation validates first and runs on a cloned state that is only
// committed on success, hence a rejected operation leaves no trace on
// leases, consents, arrears, recourse rights or the clock.
package sublease

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
)

// MonthDays is the fixed billing period length in days. Rent for period
// k (1-based) covers [start+MonthDays*(k-1), start+MonthDays*k) and is
// due at min(start+MonthDays*k, end).
const MonthDays = 30

// ErrKind classifies failures. Validation always runs in the declaration
// order below and only the first error is reported.
type ErrKind int

const (
	ErrInvalidArgument ErrKind = iota // 参数非法
	ErrClockRewind                    // 时钟回退
	ErrNotFound                       // 租约不存在或已终止
	ErrNoConsent                      // 未取得同意
	ErrTermOutOfParent                // 期限越出上级
	ErrRentCapExceeded                // 租金超过倍数上限
	ErrDepthExceeded                  // 链深度超限
	ErrStateNotAllowed                // 状态不允许该操作
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrClockRewind:
		return "clock rewind"
	case ErrNotFound:
		return "lease not found or terminated"
	case ErrNoConsent:
		return "consent not obtained"
	case ErrTermOutOfParent:
		return "term out of parent lease"
	case ErrRentCapExceeded:
		return "rent exceeds cap"
	case ErrDepthExceeded:
		return "chain depth exceeded"
	case ErrStateNotAllowed:
		return "state not allowed"
	}
	return "unknown"
}

// Error is the only failure type returned by service operations.
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Kind, e.Msg) }

func newErr(k ErrKind, format string, args ...any) *Error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, args...)}
}

// KindOf extracts the ErrKind of an error returned by this package.
func KindOf(err error) ErrKind {
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return -1
}

// Config holds the immutable policy parameters of a Service.
type Config struct {
	RentCapPercent int // P: sublease rent must be <= parent rent * P/100
	MaxDepth       int // D: maximum chain depth (master lease is depth 1)
	GraceDays      int // G: days after due date before arrears are recorded
}

func (c Config) validate() error {
	if c.RentCapPercent <= 0 {
		return newErr(ErrInvalidArgument, "rent cap percent must be positive")
	}
	if c.MaxDepth < 1 {
		return newErr(ErrInvalidArgument, "max depth must be >= 1")
	}
	if c.GraceDays < 0 {
		return newErr(ErrInvalidArgument, "grace days must be >= 0")
	}
	return nil
}

// LeaseStatus is the lifecycle state of a lease.
type LeaseStatus int

const (
	Active LeaseStatus = iota
	Expired
	Terminated
)

func (s LeaseStatus) String() string {
	switch s {
	case Active:
		return "active"
	case Expired:
		return "expired"
	case Terminated:
		return "terminated"
	}
	return "unknown"
}

// Lease is one link of a sublease chain. The master lease has no parent
// and its Receiver is the landlord; a sublease's Receiver is the parent
// lease's tenant.
type Lease struct {
	ID           string
	Receiver     string // party entitled to the rent (landlord or parent tenant)
	Tenant       string
	Parent       string // parent lease ID, "" for master/promoted leases
	Child        string // currently effective sublease ID, "" if none
	Start        int    // first day in lease (inclusive)
	End          int    // first day out of lease (exclusive)
	Rent         int64  // monthly rent
	Depth        int    // chain level, master = 1
	Status       LeaseStatus
	Recognized   bool // independently recognized by the landlord
	TerminatedAt int  // meaningful when Status != Active

	paidThrough   int // number of billing periods paid by the tenant
	arrearsIssued int // number of billing periods checked for arrears
}

// Arrears is an overdue-rent record. Remaining is the part not yet
// cleared by anyone; settlements by chain parties create recourse
// rights of exactly the settled amount.
type Arrears struct {
	ID              string
	LeaseID         string
	Period          int
	Debtor          string // tenant of the lease
	Creditor        string // receiver of the lease at creation time
	Amount          int64
	Remaining       int64
	Settled         bool   // cleared by a liable chain party
	SettledBy       string // the chain party that paid, if any
	ClearedByDebtor bool   // cleared by the debtor paying rent
	RecourseID      string // recourse right created by the settlement
}

// Recourse is a recovery right of a settling party against the actual
// debtor. Its amount always equals the settled amount.
type Recourse struct {
	ID        string
	ArrearsID string
	Creditor  string // the party that settled
	Debtor    string // the actual owing tenant
	Amount    int64
}

// consent is the landlord's consent state for one tenant.
type consent struct {
	oneTime int  // number of remaining one-time consents
	blanket bool // blanket consent active until revoked
}

// state is the whole mutable world; operations clone it and commit on
// success, so rejections never leave a trace.
type state struct {
	now         int
	leases      map[string]*Lease
	arrears     map[string]*Arrears
	recourse    map[string]*Recourse
	consents    map[string]map[string]*consent // landlord -> tenant -> consent
	byPeriod    map[string]string              // leaseID#period -> arrears ID
	leaseSeq    int
	arrearsSeq  int
	recourseSeq int
}

func newState() *state {
	return &state{
		leases:   make(map[string]*Lease),
		arrears:  make(map[string]*Arrears),
		recourse: make(map[string]*Recourse),
		consents: make(map[string]map[string]*consent),
		byPeriod: make(map[string]string),
	}
}

func (s *state) clone() *state {
	n := newState()
	n.now = s.now
	n.leaseSeq, n.arrearsSeq, n.recourseSeq = s.leaseSeq, s.arrearsSeq, s.recourseSeq
	for k, v := range s.leases {
		cp := *v
		n.leases[k] = &cp
	}
	for k, v := range s.arrears {
		cp := *v
		n.arrears[k] = &cp
	}
	for k, v := range s.recourse {
		cp := *v
		n.recourse[k] = &cp
	}
	for lk, m := range s.consents {
		nm := make(map[string]*consent, len(m))
		for tk, c := range m {
			cp := *c
			nm[tk] = &cp
		}
		n.consents[lk] = nm
	}
	for k, v := range s.byPeriod {
		n.byPeriod[k] = v
	}
	return n
}

func periodKey(leaseID string, k int) string {
	return leaseID + "#" + strconv.Itoa(k)
}

// topLandlord walks the parent chain to the root and returns the root
// lease's receiver, i.e. the landlord of the whole chain. Cost is
// O(chain depth), independent of the total number of leases.
func (s *state) topLandlord(l *Lease) string {
	for l.Parent != "" {
		l = s.leases[l.Parent]
	}
	return l.Receiver
}

// Service is the lease subletting chain and liability service. It is
// safe for concurrent use; results equal some serial execution order.
type Service struct {
	mu  sync.Mutex
	cfg Config
	st  *state
}

// NewService creates a Service with the given policy.
func NewService(cfg Config) (*Service, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Service{cfg: cfg, st: newState()}, nil
}

// Now returns the current accepted clock value.
func (s *Service) Now() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.now
}

// begin validates the clock and returns a working clone advanced to
// now (expiry cascade and arrears generation applied). The clone is
// committed by the caller only when the operation succeeds.
func (s *Service) begin(now int) (*state, error) {
	if now < 0 {
		return nil, newErr(ErrInvalidArgument, "now must be >= 0, got %d", now)
	}
	if now < s.st.now {
		return nil, newErr(ErrClockRewind, "now %d < last accepted now %d", now, s.st.now)
	}
	ns := s.st.clone()
	ns.advanceTo(now, s.cfg)
	return ns, nil
}

// commit replaces the live state with the accepted working clone.
func (s *Service) commit(ns *state) { s.st = ns }

// advanceTo moves the clock forward: leases whose end has passed expire
// (cascading to subleases), then overdue unpaid rent becomes arrears.
// Iteration is sorted so replays of the same operation sequence are
// bit-identical.
func (s *state) advanceTo(now int, cfg Config) {
	s.now = now
	var expiring []*Lease
	for _, l := range s.leases {
		if l.Status == Active && l.End <= now {
			expiring = append(expiring, l)
		}
	}
	sort.Slice(expiring, func(i, j int) bool {
		if expiring[i].Depth != expiring[j].Depth {
			return expiring[i].Depth < expiring[j].Depth
		}
		return expiring[i].ID < expiring[j].ID
	})
	for _, l := range expiring {
		if l.Status == Active {
			s.endLease(l, Expired, l.End)
		}
	}
	ids := make([]string, 0, len(s.leases))
	for id := range s.leases {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s.issueArrears(s.leases[id], now, cfg.GraceDays)
	}
}

// --- deterministic read views ---

// LeaseView is a stable external view of a lease.
type LeaseView struct {
	ID, Receiver, Tenant, Parent, Child, Status string
	Start, End, Depth, TerminatedAt             int
	Rent                                        int64
	Recognized                                  bool
}

// ArrearsView is a stable external view of an arrears record.
type ArrearsView struct {
	ID, LeaseID, Debtor, Creditor, SettledBy, RecourseID string
	Period                                               int
	Amount, Remaining                                    int64
	Settled, ClearedByDebtor                             bool
}

// RecourseView is a stable external view of a recourse right.
type RecourseView struct {
	ID, ArrearsID, Creditor, Debtor string
	Amount                          int64
}

// ConsentView is a stable external view of a consent state.
type ConsentView struct {
	Landlord, Tenant string
	OneTime          int
	Blanket          bool
}

// Snapshot is a deterministic dump of the whole observable state.
type Snapshot struct {
	Now      int
	Leases   []LeaseView
	Arrears  []ArrearsView
	Recourse []RecourseView
	Consents []ConsentView
}

// Snapshot returns the current observable state in a canonical order.
func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.snapshot()
}

func (s *state) snapshot() Snapshot {
	snap := Snapshot{Now: s.now}
	for _, l := range s.leases {
		snap.Leases = append(snap.Leases, LeaseView{
			ID: l.ID, Receiver: l.Receiver, Tenant: l.Tenant,
			Parent: l.Parent, Child: l.Child, Status: l.Status.String(),
			Start: l.Start, End: l.End, Depth: l.Depth, Rent: l.Rent,
			Recognized: l.Recognized, TerminatedAt: l.TerminatedAt,
		})
	}
	sort.Slice(snap.Leases, func(i, j int) bool { return snap.Leases[i].ID < snap.Leases[j].ID })
	for _, a := range s.arrears {
		snap.Arrears = append(snap.Arrears, ArrearsView{
			ID: a.ID, LeaseID: a.LeaseID, Debtor: a.Debtor, Creditor: a.Creditor,
			SettledBy: a.SettledBy, RecourseID: a.RecourseID, Period: a.Period,
			Amount: a.Amount, Remaining: a.Remaining,
			Settled: a.Settled, ClearedByDebtor: a.ClearedByDebtor,
		})
	}
	sort.Slice(snap.Arrears, func(i, j int) bool { return snap.Arrears[i].ID < snap.Arrears[j].ID })
	for _, r := range s.recourse {
		snap.Recourse = append(snap.Recourse, RecourseView{
			ID: r.ID, ArrearsID: r.ArrearsID, Creditor: r.Creditor,
			Debtor: r.Debtor, Amount: r.Amount,
		})
	}
	sort.Slice(snap.Recourse, func(i, j int) bool { return snap.Recourse[i].ID < snap.Recourse[j].ID })
	type ck struct{ l, t string }
	keys := make([]ck, 0, len(s.consents))
	for lk, m := range s.consents {
		for tk := range m {
			keys = append(keys, ck{lk, tk})
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].l != keys[j].l {
			return keys[i].l < keys[j].l
		}
		return keys[i].t < keys[j].t
	})
	for _, k := range keys {
		c := s.consents[k.l][k.t]
		snap.Consents = append(snap.Consents, ConsentView{
			Landlord: k.l, Tenant: k.t, OneTime: c.oneTime, Blanket: c.blanket,
		})
	}
	return snap
}
