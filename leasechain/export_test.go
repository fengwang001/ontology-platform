package leasechain

import "sort"

func (s *Service) allLeases() []*Lease {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Lease
	for _, l := range s.leases {
		cp := *l
		out = append(out, &cp)
	}
	return out
}

func (s *Service) allArrears() []*Arrear {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Arrear
	for _, a := range s.arrears {
		cp := *a
		out = append(out, &cp)
	}
	return out
}

func (s *Service) allRecourses() []*Recourse {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Recourse
	for _, r := range s.recourses {
		cp := *r
		out = append(out, &cp)
	}
	return out
}

// modelSnapshot 导出正式实现状态，字段与 NaiveModel.State 一一对应。
type modelSnapshot = Snapshot

func (s *Service) snapshotState() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := Snapshot{LastNow: s.lastNow, NextID: s.nextID}
	for _, l := range s.leases {
		cp.Leases = append(cp.Leases, naiveLease{
			id: l.ID, landlordID: l.LandlordID, tenantID: l.TenantID,
			parentID: l.ParentID, start: l.Start, end: l.End, rent: l.Rent,
			recognized: l.Recognized, terminated: l.Terminated,
		})
	}
	for _, a := range s.arrears {
		cp.Arrears = append(cp.Arrears, naiveArrear{
			id: a.ID, leaseID: a.LeaseID, due: a.Due, amount: a.Amount, paid: a.Paid,
		})
	}
	for _, r := range s.recourses {
		cp.Recourses = append(cp.Recourses, naiveRecourse{
			id: r.ID, arrearID: r.ArrearID, payerID: r.PayerID, amount: r.Amount,
		})
	}
	for _, c := range s.oneTimes {
		cp.OneTimes = append(cp.OneTimes, naiveOneTime{
			id: c.ID, landlord: c.LandlordID, tenant: c.TenantID, parentID: c.ParentID,
			start: c.Start, end: c.End, rent: c.Rent, used: c.Used,
		})
	}
	for k, g := range s.generals {
		cp.Generals = append(cp.Generals, naiveGeneral{
			landlord: g.landlord, tenant: g.tenant, revoked: g.revokedAt != 0,
		})
		_ = k
	}
	for _, b := range s.bills {
		var aid int64
		if a := s.arrearByBillLocked(b.leaseID, b.due); a != nil {
			aid = a.ID
		}
		cp.Bills = append(cp.Bills, naiveBill{
			leaseID: b.leaseID, due: b.due, amount: b.amount, arrearID: aid,
		})
	}
	sortAll(&cp)
	return cp
}

func sortAll(cp *Snapshot) {
	sort.Slice(cp.Leases, func(i, j int) bool { return cp.Leases[i].id < cp.Leases[j].id })
	sort.Slice(cp.Arrears, func(i, j int) bool { return cp.Arrears[i].id < cp.Arrears[j].id })
	sort.Slice(cp.Recourses, func(i, j int) bool { return cp.Recourses[i].id < cp.Recourses[j].id })
	sort.Slice(cp.OneTimes, func(i, j int) bool { return cp.OneTimes[i].id < cp.OneTimes[j].id })
	sort.Slice(cp.Generals, func(i, j int) bool {
		if cp.Generals[i].landlord != cp.Generals[j].landlord {
			return cp.Generals[i].landlord < cp.Generals[j].landlord
		}
		return cp.Generals[i].tenant < cp.Generals[j].tenant
	})
	sort.Slice(cp.Bills, func(i, j int) bool {
		if cp.Bills[i].leaseID != cp.Bills[j].leaseID {
			return cp.Bills[i].leaseID < cp.Bills[j].leaseID
		}
		return cp.Bills[i].due < cp.Bills[j].due
	})
}
