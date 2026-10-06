// Package naivemodel is an independently written, deliberately simple
// reference implementation of the lease/subleasing rules. It shares only the
// operation and snapshot data shapes with the real service; every rule is
// re-derived by scanning flat tables. Random operation sequences are replayed
// against both implementations and their full snapshots compared.
package naivemodel

import (
	"sort"

	"ontology/lease"
)

type Model struct {
	cfg     lease.Config
	now     int
	leases  map[lease.LeaseID]lease.Lease
	consent []lease.Consent
	debt    []lease.Arrears
	claims  []lease.Recourse
	debtSeq int
}

func New(cfg lease.Config) *Model {
	return &Model{cfg: cfg, leases: map[lease.LeaseID]lease.Lease{}, debtSeq: 1}
}

// clockGuard implements the ordering rule: invalid arguments (already
// rejected by the caller before this point) beat clock rollback; the live
// clock is tested before any domain state is touched.
func (m *Model) clockGuard(now int) error {
	if now < m.now {
		return &lease.OpError{Code: lease.ErrClockRollback}
	}
	return nil
}

func (m *Model) reject(now int, c lease.Code) error {
	if now < m.now {
		return &lease.OpError{Code: lease.ErrClockRollback}
	}
	return &lease.OpError{Code: c}
}

func (m *Model) child(parent lease.LeaseID) (lease.Lease, bool) {
	for _, l := range m.leases {
		if l.Parent == parent && l.Active {
			return l, true
		}
	}
	return lease.Lease{}, false
}

// childMinID is the deterministic-by-id variant used for the unique active
// child, matching the real service's tie-break.
func (m *Model) childMinID(parent lease.LeaseID) (lease.Lease, bool) {
	best := lease.LeaseID(0)
	found := false
	for _, l := range m.leases {
		if l.Parent == parent && l.Active && (!found || l.ID < best) {
			best, found = l.ID, true
		}
	}
	if !found {
		return lease.Lease{}, false
	}
	return m.leases[best], true
}

// cascadeFrom walks the linear descendant chain from l: a recognized child
// whose term continues past day is promoted to a new depth-1 root; below a
// promoted ancestor the rest of the chain survives and shifts up one level.
func (m *Model) cascadeFrom(l lease.Lease, day int) {
	head := m.leases[l.Root].Landlord
	cur := l
	cut, depth := true, 0
	var root lease.LeaseID
	for {
		if cut {
			cur.Active = false
			if day < cur.End {
				cur.End = day
			}
		}
		m.leases[cur.ID] = cur
		ch, has := m.childMinID(cur.ID)
		if !has {
			return
		}
		switch {
		case cut && ch.Recognized && ch.Start <= day && ch.End > day:
			ch.Parent, ch.Depth, ch.Root, ch.Landlord = 0, 1, ch.ID, head
			m.leases[ch.ID] = ch
			cut, root, depth = false, ch.ID, 2
		case cut:
			ch.Active = false
			if day < ch.End {
				ch.End = day
			}
			m.leases[ch.ID] = ch
		default:
			ch.Depth, ch.Root = depth, root
			m.leases[ch.ID] = ch
			depth++
		}
		cur = ch
	}
}

// expire repeatedly handles the earliest ending active lease.
func (m *Model) expire() {
	for {
		var next lease.Lease
		found := false
		for _, l := range m.leases {
			if l.Active && l.End <= m.now &&
				(!found || l.End < next.End || (l.End == next.End && l.ID < next.ID)) {
				next, found = l, true
			}
		}
		if !found {
			return
		}
		m.cascadeFrom(next, next.End)
	}
}

func (m *Model) existsDebt(id lease.LeaseID, due int) bool {
	for _, a := range m.debt {
		if a.Lease == id && a.Due == due {
			return true
		}
	}
	return false
}

// bill scans every lease and adds installments whose due day lies inside the
// (possibly shortened) term and that are overdue by G days at m.now.
func (m *Model) bill() {
	var ids []lease.LeaseID
	for id := range m.leases {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		l := m.leases[id]
		first, last := 0, -1
		if l.End > l.Start {
			first = l.Start / 30
			if 30*first+m.cfg.PayDay < l.Start {
				first++
			}
			last = (l.End - 1) / 30
			if 30*last+m.cfg.PayDay >= l.End {
				last--
			}
		}
		for mo := first; mo <= last; mo++ {
			due := 30*mo + m.cfg.PayDay
			if m.now < due+m.cfg.GraceDays || m.existsDebt(l.ID, due) {
				continue
			}
			chain := []lease.LeaseID{l.ID}
			for p := l.Parent; p != 0; p = m.leases[p].Parent {
				chain = append(chain, p)
			}
			m.debt = append(m.debt, lease.Arrears{
				ID: m.debtSeq, Lease: l.ID, Root: l.Root, Due: due,
				Amount: l.Rent, Chain: chain,
			})
			m.debtSeq++
		}
	}
}

func (m *Model) accept(now int) { m.now = now; m.expire(); m.bill() }

func (m *Model) findDebt(id int) int {
	for i := range m.debt {
		if m.debt[i].ID == id {
			return i
		}
	}
	return -1
}

func (m *Model) clone() *Model {
	c := &Model{cfg: m.cfg, now: m.now, debtSeq: m.debtSeq,
		leases: map[lease.LeaseID]lease.Lease{}}
	for k, v := range m.leases {
		c.leases[k] = v
	}
	c.consent = append([]lease.Consent(nil), m.consent...)
	c.debt = append([]lease.Arrears(nil), m.debt...)
	for i := range c.debt {
		c.debt[i].Chain = append([]lease.LeaseID(nil), m.debt[i].Chain...)
		c.debt[i].Payments = append([]lease.Payment(nil), m.debt[i].Payments...)
	}
	c.claims = append([]lease.Recourse(nil), m.claims...)
	return c
}

func (m *Model) CreateMaster(op lease.CreateMasterOp) error {
	if op.ID <= 0 || op.Landlord <= 0 || op.Tenant <= 0 ||
		op.Start < 0 || op.End <= op.Start || op.Rent < 0 {
		return &lease.OpError{Code: lease.ErrInvalid}
	}
	if err := m.clockGuard(op.Now); err != nil {
		return err
	}
	c := m.clone()
	if _, dup := c.leases[op.ID]; dup {
		return m.reject(op.Now, lease.ErrStateNotAllowed)
	}
	c.leases[op.ID] = lease.Lease{
		ID: op.ID, Landlord: op.Landlord, Tenant: op.Tenant,
		Start: op.Start, End: op.End, Rent: op.Rent,
		Active: true, Depth: 1, Root: op.ID,
	}
	c.accept(op.Now)
	*m = *c
	return nil
}

func (m *Model) consented(p lease.Lease, sub lease.LeaseID, now int) bool {
	gen, genAt := false, 0
	for _, c := range m.consent {
		if c.Root != p.Root || c.Granted > now {
			continue
		}
		if c.OneShot {
			if c.Lease == sub && !c.Consumed {
				return true
			}
			continue
		}
		if !c.Revoked && c.Landlord == p.Landlord && c.Tenant == p.Tenant &&
			(!gen || c.Granted > genAt) {
			gen, genAt = true, c.Granted
		}
	}
	return gen
}

func (m *Model) useConsent(p lease.Lease, sub lease.LeaseID, now int) {
	for i := range m.consent {
		c := &m.consent[i]
		if c.OneShot && c.Root == p.Root && c.Lease == sub && !c.Consumed && c.Granted <= now {
			c.Consumed = true
			return
		}
	}
}

func (m *Model) CreateSublease(op lease.CreateSubleaseOp) error {
	if op.ID <= 0 || op.Parent <= 0 || op.Tenant <= 0 ||
		op.Rent < 0 || op.End <= op.Start {
		return &lease.OpError{Code: lease.ErrInvalid}
	}
	if err := m.clockGuard(op.Now); err != nil {
		return err
	}
	c := m.clone()
	p, ok := c.leases[op.Parent]
	if !ok || !p.Active {
		return m.reject(op.Now, lease.ErrLeaseNotFound)
	}
	if !c.consented(p, op.ID, op.Now) {
		return m.reject(op.Now, lease.ErrNoConsent)
	}
	if op.Start < p.Start || op.End > p.End {
		return m.reject(op.Now, lease.ErrTermOutOfRange)
	}
	if op.Rent*100 > p.Rent*c.cfg.RentFactorPct {
		return m.reject(op.Now, lease.ErrRentExceedsLimit)
	}
	if p.Depth+1 > c.cfg.MaxDepth {
		return m.reject(op.Now, lease.ErrDepthExceeded)
	}
	if _, dup := c.leases[op.ID]; dup {
		return m.reject(op.Now, lease.ErrStateNotAllowed)
	}
	if p.Tenant == op.Tenant {
		return m.reject(op.Now, lease.ErrStateNotAllowed)
	}
	if _, has := c.childMinID(p.ID); has {
		return m.reject(op.Now, lease.ErrStateNotAllowed)
	}
	c.useConsent(p, op.ID, op.Now)
	c.leases[op.ID] = lease.Lease{
		ID: op.ID, Landlord: p.Tenant, Tenant: op.Tenant, Parent: p.ID,
		Start: op.Start, End: op.End, Rent: op.Rent,
		Active: true, Depth: p.Depth + 1, Root: p.Root,
	}
	c.accept(op.Now)
	*m = *c
	return nil
}

func (m *Model) GrantOneShot(op lease.GrantOneShotOp) error {
	if op.ID <= 0 || op.Root <= 0 || op.Landlord <= 0 {
		return &lease.OpError{Code: lease.ErrInvalid}
	}
	if err := m.clockGuard(op.Now); err != nil {
		return err
	}
	c := m.clone()
	r, ok := c.leases[op.Root]
	if !ok || !r.Active || r.Depth != 1 || r.Landlord != op.Landlord {
		return m.reject(op.Now, lease.ErrLeaseNotFound)
	}
	for _, x := range c.consent {
		if x.OneShot && x.Lease == op.ID && !x.Consumed {
			return m.reject(op.Now, lease.ErrStateNotAllowed)
		}
	}
	c.consent = append(c.consent, lease.Consent{
		OneShot: true, Landlord: op.Landlord, Lease: op.ID,
		Root: op.Root, Granted: op.Now,
	})
	c.accept(op.Now)
	*m = *c
	return nil
}

func (m *Model) GrantGeneral(op lease.GrantGeneralOp) error {
	if op.Root <= 0 || op.Landlord <= 0 || op.Tenant <= 0 {
		return &lease.OpError{Code: lease.ErrInvalid}
	}
	if err := m.clockGuard(op.Now); err != nil {
		return err
	}
	c := m.clone()
	r, ok := c.leases[op.Root]
	if !ok || !r.Active || r.Depth != 1 || r.Landlord != op.Landlord {
		return m.reject(op.Now, lease.ErrLeaseNotFound)
	}
	for _, x := range c.consent {
		if !x.OneShot && x.Root == op.Root && x.Tenant == op.Tenant && !x.Revoked {
			return m.reject(op.Now, lease.ErrStateNotAllowed)
		}
	}
	c.consent = append(c.consent, lease.Consent{
		Landlord: op.Landlord, Tenant: op.Tenant, Root: op.Root, Granted: op.Now,
	})
	c.accept(op.Now)
	*m = *c
	return nil
}

func (m *Model) RevokeGeneral(op lease.RevokeGeneralOp) error {
	if op.Root <= 0 || op.Landlord <= 0 || op.Tenant <= 0 {
		return &lease.OpError{Code: lease.ErrInvalid}
	}
	if err := m.clockGuard(op.Now); err != nil {
		return err
	}
	c := m.clone()
	idx := -1
	for i := range c.consent {
		x := &c.consent[i]
		if !x.OneShot && x.Root == op.Root && x.Tenant == op.Tenant &&
			x.Landlord == op.Landlord && !x.Revoked &&
			(idx < 0 || x.Granted > c.consent[idx].Granted) {
			idx = i
		}
	}
	if idx < 0 {
		return m.reject(op.Now, lease.ErrStateNotAllowed)
	}
	c.consent[idx].Revoked = true
	c.accept(op.Now)
	*m = *c
	return nil
}

func (m *Model) Recognize(op lease.RecognizeOp) error {
	if op.Lease <= 0 || op.Landlord <= 0 {
		return &lease.OpError{Code: lease.ErrInvalid}
	}
	if err := m.clockGuard(op.Now); err != nil {
		return err
	}
	c := m.clone()
	l, ok := c.leases[op.Lease]
	if !ok || !l.Active {
		return m.reject(op.Now, lease.ErrLeaseNotFound)
	}
	if c.leases[l.Root].Landlord != op.Landlord {
		return m.reject(op.Now, lease.ErrLeaseNotFound)
	}
	if l.Depth <= 1 || l.Recognized {
		return m.reject(op.Now, lease.ErrStateNotAllowed)
	}
	l.Recognized = true
	c.leases[l.ID] = l
	c.accept(op.Now)
	*m = *c
	return nil
}

func (m *Model) Pay(op lease.PayOp) error {
	if op.ArrearsID <= 0 || op.AtLease <= 0 || op.By <= 0 {
		return &lease.OpError{Code: lease.ErrInvalid}
	}
	if err := m.clockGuard(op.Now); err != nil {
		return err
	}
	c := m.clone()
	idx := c.findDebt(op.ArrearsID)
	if idx < 0 {
		return m.reject(op.Now, lease.ErrLeaseNotFound)
	}
	a := &c.debt[idx]
	if op.Amount <= 0 {
		return m.reject(op.Now, lease.ErrInvalid)
	}
	if a.Paid >= a.Amount {
		return m.reject(op.Now, lease.ErrStateNotAllowed)
	}
	onChain := false
	for _, id := range a.Chain {
		if id == op.AtLease {
			onChain = true
		}
	}
	if !onChain || c.leases[op.AtLease].Tenant != op.By {
		return m.reject(op.Now, lease.ErrStateNotAllowed)
	}
	if op.Amount > a.Amount-a.Paid {
		return m.reject(op.Now, lease.ErrInvalid)
	}
	a.Paid += op.Amount
	a.Payments = append(a.Payments, lease.Payment{
		Payer: op.By, Lease: op.AtLease, Amount: op.Amount, Day: op.Now,
	})
	c.claims = append(c.claims, lease.Recourse{
		ArrearsID: a.ID, From: c.leases[a.Lease].Tenant,
		To: op.By, Amount: op.Amount, Day: op.Now,
	})
	c.accept(op.Now)
	*m = *c
	return nil
}

func (m *Model) Terminate(op lease.TerminateOp) error {
	if op.Lease <= 0 || op.By <= 0 || op.Day < 0 {
		return &lease.OpError{Code: lease.ErrInvalid}
	}
	if err := m.clockGuard(op.Now); err != nil {
		return err
	}
	c := m.clone()
	l, ok := c.leases[op.Lease]
	if !ok || !l.Active {
		return m.reject(op.Now, lease.ErrLeaseNotFound)
	}
	day := op.Day
	if day == 0 {
		day = op.Now
	}
	if day < l.Start || day > op.Now {
		return m.reject(op.Now, lease.ErrInvalid)
	}
	if l.Tenant != op.By && c.leases[l.Root].Landlord != op.By {
		return m.reject(op.Now, lease.ErrStateNotAllowed)
	}
	c.cascadeFrom(l, day)
	c.accept(op.Now)
	*m = *c
	return nil
}

func (m *Model) Exit(op lease.ExitOp) error {
	if op.Lease <= 0 || op.By <= 0 {
		return &lease.OpError{Code: lease.ErrInvalid}
	}
	if err := m.clockGuard(op.Now); err != nil {
		return err
	}
	c := m.clone()
	l, ok := c.leases[op.Lease]
	if !ok || !l.Active {
		return m.reject(op.Now, lease.ErrLeaseNotFound)
	}
	if l.Tenant != op.By {
		return m.reject(op.Now, lease.ErrStateNotAllowed)
	}
	if ch, has := c.childMinID(l.ID); has && !ch.Recognized {
		return m.reject(op.Now, lease.ErrStateNotAllowed)
	}
	c.cascadeFrom(l, op.Now)
	c.accept(op.Now)
	*m = *c
	return nil
}

func (m *Model) Advance(op lease.AdvanceOp) error {
	if err := m.clockGuard(op.Now); err != nil {
		return err
	}
	c := m.clone()
	c.accept(op.Now)
	*m = *c
	return nil
}

// Snapshot produces the same shape as lease.Service.Snapshot.
func (m *Model) Snapshot() lease.Snapshot {
	snap := lease.Snapshot{
		Leases:    map[lease.LeaseID]lease.Lease{},
		Now:       m.now,
		Arrears:   []lease.Arrears{},
		Recourses: []lease.Recourse{},
		Consents:  []lease.Consent{},
	}
	for id, l := range m.leases {
		snap.Leases[id] = l
	}
	snap.Arrears = append(snap.Arrears, m.debt...)
	sort.Slice(snap.Arrears, func(i, j int) bool {
		if snap.Arrears[i].Lease != snap.Arrears[j].Lease {
			return snap.Arrears[i].Lease < snap.Arrears[j].Lease
		}
		return snap.Arrears[i].Due < snap.Arrears[j].Due
	})
	snap.Recourses = append(snap.Recourses, m.claims...)
	sort.Slice(snap.Recourses, func(i, j int) bool {
		if snap.Recourses[i].Day != snap.Recourses[j].Day {
			return snap.Recourses[i].Day < snap.Recourses[j].Day
		}
		return snap.Recourses[i].ArrearsID < snap.Recourses[j].ArrearsID
	})
	snap.Consents = append(snap.Consents, m.consent...)
	return snap
}
