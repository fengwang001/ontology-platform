package sublease

import "sort"

// endLease ends l with the given status and day, then cascades: an
// effective sublease that the landlord independently recognized is
// promoted to a direct lease with the landlord (its own subleases move
// up one level); any other effective sublease ends with the same day.
func (s *state) endLease(l *Lease, status LeaseStatus, day int) {
	l.Status = status
	l.TerminatedAt = day
	if l.Parent != "" {
		if p := s.leases[l.Parent]; p != nil && p.Child == l.ID {
			p.Child = ""
		}
	}
	if l.Child == "" {
		return
	}
	c := s.leases[l.Child]
	l.Child = ""
	if c.Recognized {
		s.promote(c)
	} else {
		s.endLease(c, status, day)
	}
}

// promote turns a recognized lease into a direct lease with the
// landlord, keeping its term and rent; its subtree moves up one level.
func (s *state) promote(l *Lease) {
	landlord := s.topLandlord(l)
	l.Parent = ""
	l.Receiver = landlord
	s.adjustDepth(l, 1)
}

func (s *state) adjustDepth(l *Lease, depth int) {
	l.Depth = depth
	if l.Child != "" {
		s.adjustDepth(s.leases[l.Child], depth+1)
	}
}

// TerminateLease actively terminates a lease at day now. All subleases
// end with the same day, except recognized ones which are promoted to
// direct leases with the landlord.
func (s *Service) TerminateLease(now int, leaseID string) error {
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
	ns.endLease(l, Terminated, now)
	s.commit(ns)
	return nil
}

// TenantExit removes a tenant from every lease it currently holds. The
// exit is rejected if any held lease has an effective sublease that the
// landlord has not independently recognized. Arrears and recourse
// rights of the tenant survive the exit.
func (s *Service) TenantExit(now int, tenant string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tenant == "" {
		return newErr(ErrInvalidArgument, "tenant must be non-empty")
	}
	ns, err := s.begin(now)
	if err != nil {
		return err
	}
	var held []*Lease
	for _, l := range ns.leases {
		if l.Status == Active && l.Tenant == tenant {
			held = append(held, l)
		}
	}
	sort.Slice(held, func(i, j int) bool { return held[i].ID < held[j].ID })
	if len(held) == 0 {
		return newErr(ErrStateNotAllowed, "tenant %q holds no active lease", tenant)
	}
	for _, l := range held {
		if l.Child != "" && !ns.leases[l.Child].Recognized {
			return newErr(ErrStateNotAllowed,
				"tenant %q has unrecognized effective sublease %q under %q", tenant, l.Child, l.ID)
		}
	}
	for _, l := range held {
		if l.Status == Active {
			ns.endLease(l, Terminated, now)
		}
	}
	s.commit(ns)
	return nil
}
