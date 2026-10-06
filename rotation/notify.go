package rotation

import "sort"

// generateNotices creates one notice per non-exempt member of each selected
// group. Cost is proportional only to the members of selected groups; it never
// scans the whole region. At most one notice per user per slot is kept, so
// repeated reconciliation is idempotent.
func (st *regionState) generateNotices(groups []int, dest map[string]*slotNotice) {
	for _, gid := range groups {
		for _, uid := range st.region.groups[gid].members {
			u := st.region.users[uid]
			if u.category == Exempt {
				continue
			}
			if _, exists := dest[uid]; exists {
				continue
			}
			limit := int64(0)
			if u.category == Reserved {
				limit = u.reservedPower
			}
			dest[uid] = &slotNotice{limit: limit}
		}
	}
}

// withdrawNotices removes the notices of dropped groups. Already-confirmed
// withdrawals carry no assessment; the notice simply disappears.
func (st *regionState) withdrawNotices(from map[string]*slotNotice, groups []int) {
	set := map[int]bool{}
	for _, g := range groups {
		set[g] = true
	}
	if len(set) == 0 {
		return
	}
	for uid := range from {
		u := st.region.users[uid]
		if u != nil && set[u.groupID] {
			from[uid].withdrawn = true
			delete(from, uid)
		}
	}
}

// assessCurrent appends an unconfirmed assessment for every outstanding
// current-slot notice. Called once, when the slot becomes immutable at its
// start; notices withdrawn before that moment are absent and not assessed.
func (st *regionState) assessCurrent(s *Scheduler) {
	for uid, n := range st.curNotices {
		if n.confirmed || n.withdrawn {
			continue
		}
		s.assessments = append(s.assessments, Assessment{
			UserID: uid, RegionID: st.region.ID, SlotStart: st.curStart,
		})
	}
}

// Confirm acknowledges a user's notice for the slot containing the current
// time, or for the pre-notified next slot. Confirmation strictly before a
// slot starts is accepted; at or after its start it returns 已过截止.
func (s *Scheduler) Confirm(userID, regionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.requireRegion(regionID)
	if err != nil {
		return err
	}
	u, ok := st.region.users[userID]
	if !ok {
		return errf(RejectInvalid, "user %q not found", userID)
	}
	_ = u
	// Current slot: its confirmation deadline (the slot start) has arrived.
	if n, ok := st.curNotices[userID]; ok {
		if s.now < st.curStart {
			n.confirmed = true
			s.log("Confirm user=%s slot=%d -> ok (before start)", userID, st.curStart)
			return nil
		}
		s.log("Confirm user=%s slot=%d REJECT after-deadline now=%d", userID, st.curStart, s.now)
		return errf(RejectAfterDeadline, "已过截止: slot %d started at %d, now %d", st.curStart, st.curStart, s.now)
	}
	// Next slot: any time strictly before nextStart is in time.
	if n, ok := st.nextNotices[userID]; ok {
		if s.now < st.nextStart {
			n.confirmed = true
			s.log("Confirm user=%s slot=%d -> ok (pre-notified window)", userID, st.nextStart)
			return nil
		}
		s.log("Confirm user=%s slot=%d REJECT after-deadline now=%d", userID, st.nextStart, s.now)
		return errf(RejectAfterDeadline, "已过截止: slot %d started, now %d", st.nextStart, s.now)
	}
	s.log("Confirm user=%s REJECT no-notice", userID)
	return errf(RejectNoNotice, "通知不存在: no active notice for user %q", userID)
}

// --- read-only views --------------------------------------------------------

// Now returns the current clock.
func (s *Scheduler) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Accrued returns a group's cumulative limited duration.
func (s *Scheduler) Accrued(regionID string, groupID int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.requireRegion(regionID)
	if err != nil {
		return 0, err
	}
	g, ok := st.region.groups[groupID]
	if !ok {
		return 0, errf(RejectInvalid, "group %d not found in region %s", groupID, regionID)
	}
	return g.accrued, nil
}

// CurrentSelection returns the selected group ids for the current slot.
func (s *Scheduler) CurrentSelection(regionID string) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.requireRegion(regionID)
	if err != nil {
		return nil, err
	}
	return append([]int(nil), st.curPick...), nil
}

// NextSelection returns the pre-picked groups for the following slot.
func (s *Scheduler) NextSelection(regionID string) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.requireRegion(regionID)
	if err != nil {
		return nil, err
	}
	return append([]int(nil), st.nextPick...), nil
}

// PendingNotices returns the not-yet-confirmed or confirmed notices of the
// pre-notified slot (the only list users can currently act on).
func (s *Scheduler) PendingNotices(regionID string) ([]Notice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.requireRegion(regionID)
	if err != nil {
		return nil, err
	}
	return st.noticeList(st.nextNotices, st.nextStart), nil
}

// CurrentNotices returns the current slot's notice list (including limits).
func (s *Scheduler) CurrentNotices(regionID string) ([]Notice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.requireRegion(regionID)
	if err != nil {
		return nil, err
	}
	return st.noticeList(st.curNotices, st.curStart), nil
}

func (st *regionState) noticeList(m map[string]*slotNotice, slotStart int64) []Notice {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Notice, 0, len(ids))
	for _, id := range ids {
		out = append(out, Notice{
			UserID: id, RegionID: st.region.ID, SlotStart: slotStart, Limit: m[id].limit,
		})
	}
	return out
}

// Assessments returns a copy of all recorded unconfirmed assessments.
func (s *Scheduler) Assessments() []Assessment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Assessment(nil), s.assessments...)
}

// Decisions returns the frozen group selection for a slot (nil if not frozen).
func (s *Scheduler) Decisions(regionID string, slotStart int64) ([]int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.decisions[regionID]
	if !ok {
		return nil, false
	}
	v, ok := d[slotStart]
	return append([]int(nil), v...), ok
}
