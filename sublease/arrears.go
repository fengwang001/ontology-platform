package sublease

import "fmt"

// issueArrears records arrears for every billing period of l whose due
// date passed the grace window by now and which the tenant has not
// paid. Periods are scanned once per lease (arrearsIssued cursor), so
// replays are deterministic and no record is created twice.
func (s *state) issueArrears(l *Lease, now, grace int) {
	effEnd := l.End
	if l.Status != Active && l.TerminatedAt < effEnd {
		effEnd = l.TerminatedAt
	}
	for k := l.arrearsIssued + 1; ; k++ {
		pStart := l.Start + MonthDays*(k-1)
		if pStart >= effEnd {
			break
		}
		due := l.Start + MonthDays*k
		if due > effEnd {
			due = effEnd
		}
		if due+grace > now {
			break
		}
		l.arrearsIssued = k
		if k <= l.paidThrough {
			continue
		}
		s.arrearsSeq++
		id := fmt.Sprintf("A%d", s.arrearsSeq)
		s.arrears[id] = &Arrears{
			ID: id, LeaseID: l.ID, Period: k,
			Debtor: l.Tenant, Creditor: l.Receiver,
			Amount: l.Rent, Remaining: l.Rent,
		}
		s.byPeriod[periodKey(l.ID, k)] = id
	}
}

// PayRent pays the tenant's earliest unpaid due billing period of a
// lease. If an arrears record already exists for that period it is
// cleared by the debtor and no recourse right arises.
func (s *Service) PayRent(now int, leaseID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if leaseID == "" {
		return newErr(ErrInvalidArgument, "lease id must be non-empty")
	}
	ns, err := s.begin(now)
	if err != nil {
		return err
	}
	l, ok := ns.leases[leaseID]
	if !ok || l.Status != Active {
		return newErr(ErrNotFound, "lease %q not found or not active", leaseID)
	}
	k := l.paidThrough + 1
	if l.Start+MonthDays*(k-1) >= l.End {
		return newErr(ErrStateNotAllowed, "lease %q has no further billing period", leaseID)
	}
	due := l.Start + MonthDays*k
	if due > l.End {
		due = l.End
	}
	if due > now {
		return newErr(ErrStateNotAllowed, "period %d of lease %q is not due until day %d", k, leaseID, due)
	}
	l.paidThrough = k
	if aid, ok := ns.byPeriod[periodKey(l.ID, k)]; ok {
		if a := ns.arrears[aid]; a.Remaining > 0 {
			a.Remaining = 0
			a.ClearedByDebtor = true
		}
	}
	s.commit(ns)
	return nil
}

// SettleArrears lets a jointly liable upper-chain tenant pay the full
// remaining amount of an arrears record. The settlement clears the
// arrears for every level below and creates exactly one recourse right
// of the settling party against the actual debtor, equal to the paid
// amount. Settling an already cleared record is rejected.
func (s *Service) SettleArrears(now int, arrearsID, payer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if arrearsID == "" || payer == "" {
		return newErr(ErrInvalidArgument, "arrears id and payer must be non-empty")
	}
	ns, err := s.begin(now)
	if err != nil {
		return err
	}
	a, ok := ns.arrears[arrearsID]
	if !ok {
		return newErr(ErrNotFound, "arrears %q not found", arrearsID)
	}
	if a.Remaining == 0 {
		return newErr(ErrStateNotAllowed, "arrears %q already cleared", arrearsID)
	}
	if !ns.isAncestorTenant(a.LeaseID, payer) {
		return newErr(ErrStateNotAllowed, "%q is not a liable chain party of arrears %q", payer, arrearsID)
	}
	paid := a.Remaining
	a.Remaining = 0
	a.Settled = true
	a.SettledBy = payer
	ns.recourseSeq++
	rid := fmt.Sprintf("R%d", ns.recourseSeq)
	ns.recourse[rid] = &Recourse{
		ID: rid, ArrearsID: a.ID, Creditor: payer, Debtor: a.Debtor, Amount: paid,
	}
	a.RecourseID = rid
	s.commit(ns)
	return nil
}

// isAncestorTenant reports whether party is the tenant of any lease
// above leaseID in the chain (i.e. a jointly liable upper tenant).
// Cost is O(chain depth).
func (s *state) isAncestorTenant(leaseID, party string) bool {
	l := s.leases[leaseID]
	for l != nil && l.Parent != "" {
		l = s.leases[l.Parent]
		if l.Tenant == party {
			return true
		}
	}
	return false
}

// RespInfo describes who currently answers for an arrears record and
// who may recover from the debtor.
type RespInfo struct {
	Debtor            string   // actual owing tenant
	Liable            []string // debtor, then upper tenants up to the landlord
	Remaining         int64    // amount not yet cleared by anyone
	SettledBy         string   // chain party that settled, if any
	RecourseCreditors []string // parties holding recourse against the debtor
	Hops              int      // parent-pointer hops used; proof of O(depth) cost
}

// Responsibility resolves the liable parties and recourse creditors of
// one arrears record. It walks only the parent chain of the owing
// lease, so its cost is O(chain depth) and never grows with the total
// number of leases; Hops reports the exact number of pointer hops.
func (s *Service) Responsibility(arrearsID string) (RespInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.st.arrears[arrearsID]
	if !ok {
		return RespInfo{}, newErr(ErrNotFound, "arrears %q not found", arrearsID)
	}
	info := RespInfo{Debtor: a.Debtor, Remaining: a.Remaining, SettledBy: a.SettledBy}
	info.Liable = append(info.Liable, a.Debtor)
	cur := s.st.leases[a.LeaseID]
	for cur.Parent != "" {
		cur = s.st.leases[cur.Parent]
		info.Hops++
		info.Liable = append(info.Liable, cur.Tenant)
	}
	info.Liable = append(info.Liable, cur.Receiver) // the landlord
	if a.RecourseID != "" {
		info.RecourseCreditors = append(info.RecourseCreditors, s.st.recourse[a.RecourseID].Creditor)
	}
	return info, nil
}
