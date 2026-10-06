package hemo

import "sort"

// placementRequest is one treatment to place within an atomic admission.
// locked forces keeping a specific chair while it remains feasible.
type placementRequest struct {
	item   *Treatment
	locked string
}

func (s *System) beginAdmission() {
	s.pending = map[string][]*Treatment{}
	s.patientPending = map[string][]*Treatment{}
}

func (s *System) discardAdmission() {
	s.pending = nil
	s.patientPending = nil
}

func (s *System) commitAdmission(items []*Treatment) {
	for _, it := range items {
		s.treats[it.ID] = it
		s.chairTimelines[it.ChairID].Add(it)
		s.patientTimelines[it.PatientID].Add(it)
	}
	s.pending = nil
	s.patientPending = nil
}

func (s *System) addPending(it *Treatment) {
	s.pending[it.ChairID] = append(s.pending[it.ChairID], it)
	s.patientPending[it.PatientID] = append(s.patientPending[it.PatientID], it)
}

// admit places all requests atomically.
//
//  1. Every request is patient-validated (overlap / minimum recovery);
//     failure aborts everything with ErrPatientConflict.
//  2. If no request carries a locked chair, the same-chair preference rule
//     applies: chairs are tried in ascending ID and the first chair able to
//     host ALL requests wins for every request.
//  3. Otherwise requests are processed in ascending (Start, input order),
//     each keeping its locked chair while feasible, otherwise taking the
//     smallest feasible chair.
//
// On failure nothing changes and the highest-priority reject classification
// is returned (isolation conflict if no eligible/enabled chair exists at
// all, otherwise no feasible chair).
func (s *System) admit(requests []*placementRequest) error {
	s.beginAdmission()
	defer s.discardAdmission()

	chairIDs := sortedChairIDs(s.chairs)

	for _, req := range requests {
		if t := s.patientConflict(req.item.PatientID, req.item); t != nil {
			s.discardAdmission()
			return errf(ErrPatientConflict,
				"patient %s treatment at %d too close to treatment %s",
				req.item.PatientID, req.item.Start, t.ID)
		}
	}

	anyLocked := false
	for _, req := range requests {
		if req.locked != "" {
			anyLocked = true
		}
	}

	if !anyLocked {
		if id := s.sameChairWinner(requests, chairIDs); id != "" {
			for _, req := range requests {
				req.item.ChairID = id
				s.addPending(req.item)
			}
			committed := make([]*Treatment, len(requests))
			for i, req := range requests {
				committed[i] = req.item
			}
			s.commitAdmission(committed)
			return nil
		}
		// Reset the admission before greedy; same-chair trials must not leak.
		s.discardAdmission()
		s.beginAdmission()
	}

	order := make([]int, len(requests))
	for i := range requests {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := requests[order[a]], requests[order[b]]
		if x.item.Start != y.item.Start {
			return x.item.Start < y.item.Start
		}
		return order[a] < order[b]
	})

	placed := make([]*Treatment, 0, len(requests))
	for _, idx := range order {
		req := requests[idx]
		if req.locked != "" {
			if c := s.chairs[req.locked]; c != nil &&
				c.AvailableAt(req.item.Start) &&
				zoneEligible(c, req.item.InfectionAtStart) &&
				s.rejectReason(c, req.item, s.pending[req.locked]) == "" {
				req.item.ChairID = req.locked
				s.addPending(req.item)
				placed = append(placed, req.item)
				continue
			}
		}

		chosen := s.smallestFeasibleChair(req.item, chairIDs)
		if chosen == "" {
			// Abort whole admission. Nothing was committed, so only the
			// pending trial needs to be discarded.
			for _, it := range placed {
				it.ChairID = ""
			}
			s.discardAdmission()
			if !s.existsEligibleChair(req.item, chairIDs) {
				return errf(ErrIsolationConflict,
					"patient %s infection %s: no eligible enabled chair for treatment at %d",
					req.item.PatientID, req.item.InfectionAtStart, req.item.Start)
			}
			return errf(ErrNoFeasibleChair,
				"patient %s no feasible chair for treatment at %d",
				req.item.PatientID, req.item.Start)
		}
		req.item.ChairID = chosen
		s.addPending(req.item)
		placed = append(placed, req.item)
	}

	s.commitAdmission(placed)
	return nil
}

// sameChairWinner returns the smallest chair ID able to host every request,
// evaluated purely as a feasibility trial without touching admission state.
func (s *System) sameChairWinner(requests []*placementRequest, chairIDs []string) string {
	items := make([]*Treatment, len(requests))
	for i, req := range requests {
		items[i] = req.item
	}
	for _, id := range chairIDs {
		c := s.chairs[id]
		var local []*Treatment
		ok := true
		for _, it := range items {
			if !c.AvailableAt(it.Start) || !zoneEligible(c, it.InfectionAtStart) ||
				s.rejectReason(c, it, local) != "" {
				ok = false
				break
			}
			local = append(local, it)
		}
		if ok {
			return id
		}
	}
	return ""
}

// smallestFeasibleChair returns the smallest ID chair able to host item.
func (s *System) smallestFeasibleChair(item *Treatment, chairIDs []string) string {
	for _, id := range chairIDs {
		c := s.chairs[id]
		if s.rejectReason(c, item, s.pending[id]) == "" {
			return id
		}
	}
	return ""
}

// existsEligibleChair reports whether any enabled chair matches the zone /
// observation requirements of item (used to classify the failure code).
func (s *System) existsEligibleChair(item *Treatment, chairIDs []string) bool {
	for _, id := range chairIDs {
		c := s.chairs[id]
		if c.AvailableAt(item.Start) && zoneEligible(c, item.InfectionAtStart) {
			return true
		}
	}
	return false
}
