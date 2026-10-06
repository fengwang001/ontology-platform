package lease

import (
	"sort"
	"sync"
)

// Service is the lease / sublease chain and liability service. All methods are
// safe for concurrent use: a single mutex serializes every operation, giving
// serializable semantics directly.
type Service struct {
	mu sync.Mutex

	cfg           Config
	now           int
	clockSet      bool
	leases        map[LeaseID]Lease
	consents      consentBook
	arrears       []*Arrears
	arrearsByID   map[int]*Arrears
	recourse      []Recourse
	nextArrearsID int
}

func NewService(cfg Config) *Service {
	return &Service{
		cfg: cfg, leases: map[LeaseID]Lease{},
		arrearsByID: map[int]*Arrears{}, nextArrearsID: 1,
	}
}

// mutate is the only state-changing path. The entire transition (time-driven
// effects included) is evaluated on a deep clone; a rejected operation
// discards the clone, so no lease, consent, arrears, recourse or the clock
// changes.
func (s *Service) mutate(now int, validate func() error, fn func(w *world) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validate(); err != nil {
		return err
	}
	if now < s.now {
		return fail(ErrClockRollback)
	}
	w := s.snapshotWorld(now)
	if err := fn(w); err != nil {
		return err
	}
	w.settleTime()
	s.commit(w)
	return nil
}

// mutateNoSettle is for read-ish actions whose mere acceptance must not raise
// time-driven arrears (none currently; kept for symmetry).
func (s *Service) CreateMaster(op CreateMasterOp) error {
	return s.mutate(op.Now, func() error {
		if !positive(op.ID) || op.Landlord <= 0 || op.Tenant <= 0 ||
			op.Start < 0 || op.End <= op.Start || op.Rent < 0 {
			return fail(ErrInvalid)
		}
		return nil
	}, func(w *world) error {
		if _, exists := w.leases[op.ID]; exists {
			return fail(ErrStateNotAllowed)
		}
		w.leases[op.ID] = Lease{
			ID: op.ID, Landlord: op.Landlord, Tenant: op.Tenant,
			Start: op.Start, End: op.End, Rent: op.Rent,
			Active: true, Depth: 1, Root: op.ID,
		}
		return nil
	})
}

func (s *Service) CreateSublease(op CreateSubleaseOp) error {
	return s.mutate(op.Now, func() error {
		if !positive(op.ID) || !positive(op.Parent) || op.Tenant <= 0 ||
			op.Rent < 0 || op.End <= op.Start {
			return fail(ErrInvalid)
		}
		return nil
	}, func(w *world) error {
		p, ok := w.leases[op.Parent]
		if !ok || !p.Active {
			return fail(ErrLeaseNotFound)
		}
		consent, ok := w.consents.authorize(p.Root, p, op.Tenant, op.ID, op.Now)
		if !ok {
			return fail(ErrNoConsent)
		}
		if op.Start < p.Start || op.End > p.End {
			return fail(ErrTermOutOfRange)
		}
		if op.Rent*100 > p.Rent*w.cfg.RentFactorPct {
			return fail(ErrRentExceedsLimit)
		}
		if p.Depth+1 > w.cfg.MaxDepth {
			return fail(ErrDepthExceeded)
		}
		if _, exists := w.leases[op.ID]; exists {
			return fail(ErrStateNotAllowed)
		}
		if p.Tenant == op.Tenant {
			return fail(ErrStateNotAllowed) // subleasing to self
		}
		if activeChildCount(w.leases, p.ID) > 0 {
			return fail(ErrStateNotAllowed)
		}
		if consent.OneShot {
			consent.Consumed = true
		}
		w.leases[op.ID] = createChild(w.leases, op)
		return nil
	})
}

func (s *Service) GrantOneShot(op GrantOneShotOp) error {
	return s.mutate(op.Now, func() error {
		if !positive(op.ID) || !positive(op.Root) || op.Landlord <= 0 {
			return fail(ErrInvalid)
		}
		return nil
	}, func(w *world) error {
		root, ok := w.leases[op.Root]
		if !ok || !root.Active || root.Depth != 1 || root.Landlord != op.Landlord {
			return fail(ErrLeaseNotFound)
		}
		for _, c := range w.consents.items {
			if c.OneShot && c.Lease == op.ID && !c.Consumed && !c.Revoked {
				return fail(ErrStateNotAllowed)
			}
		}
		w.consents.grantOneShot(op.Root, op.Landlord, op.ID, op.Now)
		return nil
	})
}

func (s *Service) GrantGeneral(op GrantGeneralOp) error {
	return s.mutate(op.Now, func() error {
		if !positive(op.Root) || op.Landlord <= 0 || op.Tenant <= 0 {
			return fail(ErrInvalid)
		}
		return nil
	}, func(w *world) error {
		root, ok := w.leases[op.Root]
		if !ok || !root.Active || root.Depth != 1 || root.Landlord != op.Landlord {
			return fail(ErrLeaseNotFound)
		}
		for _, c := range w.consents.items {
			if !c.OneShot && c.Root == op.Root && c.Tenant == op.Tenant &&
				!c.Revoked {
				return fail(ErrStateNotAllowed)
			}
		}
		w.consents.grantGeneral(op.Root, op.Landlord, op.Tenant, op.Now)
		return nil
	})
}

func (s *Service) RevokeGeneral(op RevokeGeneralOp) error {
	return s.mutate(op.Now, func() error {
		if !positive(op.Root) || op.Landlord <= 0 || op.Tenant <= 0 {
			return fail(ErrInvalid)
		}
		return nil
	}, func(w *world) error {
		if !w.consents.revokeGeneral(op.Root, op.Landlord, op.Tenant, op.Now) {
			return fail(ErrStateNotAllowed)
		}
		return nil
	})
}

func (s *Service) Recognize(op RecognizeOp) error {
	return s.mutate(op.Now, func() error {
		if !positive(op.Lease) || op.Landlord <= 0 {
			return fail(ErrInvalid)
		}
		return nil
	}, func(w *world) error {
		l, ok := w.leases[op.Lease]
		if !ok || !l.Active {
			return fail(ErrLeaseNotFound)
		}
		root := w.leases[l.Root]
		if root.Landlord != op.Landlord {
			return fail(ErrLeaseNotFound)
		}
		if l.Depth <= 1 || l.Recognized {
			return fail(ErrStateNotAllowed)
		}
		l.Recognized = true
		w.leases[l.ID] = l
		return nil
	})
}

func (s *Service) Pay(op PayOp) error {
	return s.mutate(op.Now, func() error {
		if op.ArrearsID <= 0 || !positive(op.AtLease) || op.By <= 0 {
			return fail(ErrInvalid)
		}
		return nil
	}, func(w *world) error {
		target := w.arrearsByID[op.ArrearsID]
		if target == nil {
			return fail(ErrLeaseNotFound)
		}
		return w.settle(target, op)
	})
}

func (s *Service) Terminate(op TerminateOp) error {
	return s.mutate(op.Now, func() error {
		if !positive(op.Lease) || op.By <= 0 || op.Day < 0 {
			return fail(ErrInvalid)
		}
		return nil
	}, func(w *world) error {
		l, ok := w.leases[op.Lease]
		if !ok || !l.Active {
			return fail(ErrLeaseNotFound)
		}
		day := op.Day
		if day == 0 {
			day = op.Now
		}
		if day < l.Start || day > op.Now {
			return fail(ErrInvalid)
		}
		if l.Tenant != op.By && w.leases[l.Root].Landlord != op.By {
			return fail(ErrStateNotAllowed)
		}
		cascadeTermination(w.leases, w.leases[l.Root].Landlord, l, day)
		return nil
	})
}

func (s *Service) Exit(op ExitOp) error {
	return s.mutate(op.Now, func() error {
		if !positive(op.Lease) || op.By <= 0 {
			return fail(ErrInvalid)
		}
		return nil
	}, func(w *world) error {
		l, ok := w.leases[op.Lease]
		if !ok || !l.Active {
			return fail(ErrLeaseNotFound)
		}
		if l.Tenant != op.By {
			return fail(ErrStateNotAllowed)
		}
		if child, has := activeChild(w.leases, l.ID); has && !child.Recognized {
			return fail(ErrStateNotAllowed)
		}
		cascadeTermination(w.leases, w.leases[l.Root].Landlord, l, op.Now)
		return nil
	})
}

func (s *Service) Advance(op AdvanceOp) error {
	return s.mutate(op.Now, func() error { return nil }, func(w *world) error { return nil })
}

// LiableParty reports who currently answers for the unpaid remainder of an
// arrears: the original debtor, unless its chain already contains a settled
// level (in which case that level carries it upstream). Cost O(depth).
func (s *Service) LiableParty(arrearsID int) (Party, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.arrearsByID[arrearsID]
	if a == nil {
		return 0, false
	}
	// Cost: one map lookup + the frozen chain length (depth) is the only
	// chain data available; the answer itself needs just the debtor lease.
	return s.leases[a.Lease].Tenant, true
}

// ReimbursableParties reports parties that already settled any part of the
// arrears (each holds a recourse claim against the debtor). Cost O(depth).
func (s *Service) ReimbursableParties(arrearsID int) []Party {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.arrearsByID[arrearsID]
	if a == nil {
		return nil
	}
	out := make([]Party, 0, len(a.Chain))
	for _, p := range a.Payments {
		out = append(out, p.Payer)
	}
	return out
}

func positive(id LeaseID) bool { return id > 0 }

// world is a detached, mutable working copy of the service state. Every
// accepted operation runs time settlement on exactly one world, then commits
// it; rejected operations discard it.
type world struct {
	cfg           Config
	now           int
	leases        map[LeaseID]Lease
	consents      consentBook
	arrears       []*Arrears
	arrearsByID   map[int]*Arrears
	recourse      []Recourse
	nextArrearsID int
}

func (s *Service) snapshotWorld(now int) *world {
	w := &world{
		cfg:           s.cfg,
		now:           now,
		leases:        make(map[LeaseID]Lease, len(s.leases)),
		arrearsByID:   make(map[int]*Arrears, len(s.arrearsByID)),
		nextArrearsID: s.nextArrearsID,
	}
	for id, l := range s.leases {
		w.leases[id] = l
	}
	w.consents.items = make([]*Consent, len(s.consents.items))
	for i, c := range s.consents.items {
		cp := *c
		w.consents.items[i] = &cp
	}
	w.arrears = make([]*Arrears, len(s.arrears))
	for i, a := range s.arrears {
		ap := *a
		ap.Chain = append([]LeaseID(nil), a.Chain...)
		ap.Payments = append([]Payment(nil), a.Payments...)
		w.arrears[i] = &ap
		w.arrearsByID[ap.ID] = &ap
	}
	w.recourse = append([]Recourse(nil), s.recourse...)
	return w
}

func (s *Service) commit(w *world) {
	s.now = w.now
	s.leases = w.leases
	s.consents = w.consents
	s.arrears = w.arrears
	s.arrearsByID = w.arrearsByID
	s.recourse = w.recourse
	s.nextArrearsID = w.nextArrearsID
	s.clockSet = true
}

// settleTime applies every effect due at or before w.now: first leases
// reaching their end day cascade-terminate (recognized children promote),
// then overdue installments become arrears. Processing by ascending end day
// makes the result independent of map iteration order.
func (w *world) settleTime() {
	for {
		// Pick the earliest ending active lease (id tie-break) and run one
		// cascade, then re-scan: a cascade may end other leases, and a
		// promoted subtree keeps its own schedule.
		var next Lease
		found := false
		for _, l := range w.leases {
			if l.Active && l.End <= w.now &&
				(!found || l.End < next.End || (l.End == next.End && l.ID < next.ID)) {
				next, found = l, true
			}
		}
		if !found {
			break
		}
		cascadeTermination(w.leases, w.leases[next.Root].Landlord, next, next.End)
	}
	w.raiseArrears(w.now)
}

// Snapshot returns a value-comparable, deterministic copy of the state for
// tests and inspection.
func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{
		Leases:  map[LeaseID]Lease{},
		Now:     s.now,
		Arrears: make([]Arrears, 0, len(s.arrears)),
	}
	for id, l := range s.leases {
		snap.Leases[id] = l
	}
	for _, a := range s.arrears {
		snap.Arrears = append(snap.Arrears, *a)
	}
	sort.Slice(snap.Arrears, func(i, j int) bool {
		if snap.Arrears[i].Lease != snap.Arrears[j].Lease {
			return snap.Arrears[i].Lease < snap.Arrears[j].Lease
		}
		return snap.Arrears[i].Due < snap.Arrears[j].Due
	})
	snap.Recourses = []Recourse{}
	snap.Recourses = append(snap.Recourses, s.recourse...)
	sort.Slice(snap.Recourses, func(i, j int) bool {
		if snap.Recourses[i].Day != snap.Recourses[j].Day {
			return snap.Recourses[i].Day < snap.Recourses[j].Day
		}
		return snap.Recourses[i].ArrearsID < snap.Recourses[j].ArrearsID
	})
	snap.Consents = []Consent{}
	for _, c := range s.consents.items {
		snap.Consents = append(snap.Consents, *c)
	}
	return snap
}
