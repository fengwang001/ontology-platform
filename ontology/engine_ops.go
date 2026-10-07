package ontology

import "fmt"

// applyContribution adds (sign=+1) or removes (sign=-1) one membership's
// contribution from a group aggregate.
func (vs *viewState) applyContribution(groupID string, val float64, present bool, sign float64) {
	g := vs.ensureGroup(groupID)
	g.sum += sign * val
	if present {
		if sign > 0 {
			g.count++
		} else {
			g.count--
		}
	}
}

func newMembership() *membership {
	return &membership{contrib: map[string]float64{}, present: map[string]bool{}}
}

func (vs *viewState) ensureMember(aggID string) *membership {
	m := vs.member[aggID]
	if m == nil {
		m = newMembership()
		vs.member[aggID] = m
	}
	return m
}

func (m *membership) attach(groupID string, val float64, present bool) {
	if !contains(m.groups, groupID) {
		m.groups = append(m.groups, groupID)
	}
	m.contrib[groupID] = val
	m.present[groupID] = present
}

func (m *membership) detach(groupID string) (float64, bool) {
	val, pres := m.contrib[groupID], m.present[groupID]
	delete(m.contrib, groupID)
	delete(m.present, groupID)
	out := m.groups[:0]
	for _, g := range m.groups {
		if g != groupID {
			out = append(out, g)
		}
	}
	m.groups = out
	return val, pres
}

// SetMemberProperty writes the aggregated property and, in the same unit,
// updates exactly the aggregates of the groups the instance currently belongs
// to; no unrelated group is scanned or touched.
func (e *Engine) SetMemberProperty(view, aggID string, v Optional) (OpResult, error) {
	return e.runUnit("setMemberProperty", func() (OpResult, error) {
		vd, vs, err := e.viewLocked(view)
		if err != nil {
			return OpResult{}, err
		}
		if !e.store.ObjectExists(vd.AggType, aggID) {
			return OpResult{}, classified(ClassTypeNotParticipating, "setMemberProperty",
				fmt.Sprintf("instance %q is not of aggregated type %q declared by view %q", aggID, vd.AggType, vd.Name))
		}
		old := e.valueOf(vd, aggID)
		m := vs.ensureMember(aggID)
		touched := append([]string(nil), m.groups...)
		delta := 0.0
		if v.Present {
			delta += v.Value
		}
		if old.Present {
			delta -= old.Value
		}
		presDelta := boolToInt(v.Present) - boolToInt(old.Present)
		input := fmt.Sprintf("aggType=%s aggID=%s property=%s old=%s new=%s",
			vd.AggType, aggID, vd.ValueProperty, optString(old), optString(v))
		rationale := fmt.Sprintf("adjust each current membership by delta=%v countDelta=%d; only %d current group(s) touched, no rescan",
			delta, presDelta, len(touched))
		return e.commit(vd, vs, "setMemberProperty", input, rationale, touched, func() {
			if perr := e.store.SetProperty(vd.AggType, aggID, vd.ValueProperty, v); perr != nil {
				panic(txnPanic{})
			}
			for _, g := range m.groups {
				ga := vs.ensureGroup(g)
				ga.sum += delta
				ga.count += presDelta
				m.contrib[g] = 0
				m.present[g] = false
				if v.Present {
					m.contrib[g] = v.Value
					m.present[g] = true
				}
			}
		}), nil
	})
}

// AddToGroup creates one membership link.
func (e *Engine) AddToGroup(view, aggID, groupID string) (OpResult, error) {
	return e.runUnit("addToGroup", func() (OpResult, error) {
		vd, vs, err := e.viewLocked(view)
		if err != nil {
			return OpResult{}, err
		}
		if err := e.requireGroupAndMember(vd, "addToGroup", groupID, aggID); err != nil {
			return OpResult{}, err
		}
		m := vs.ensureMember(aggID)
		if vd.Contribution == PolicyDenyMulti && !contains(m.groups, groupID) && len(m.groups) >= 1 {
			return OpResult{}, classified(ClassPolicyRejected, "addToGroup",
				fmt.Sprintf("view %q forbids simultaneous multi-group membership; %q already belongs to %v", vd.Name, aggID, m.groups))
		}
		if contains(m.groups, groupID) {
			return OpResult{}, classified(ClassInvalid, "addToGroup", "membership already exists")
		}
		opt := e.valueOf(vd, aggID)
		val, _ := participation(opt)
		input := fmt.Sprintf("aggType=%s aggID=%s groupType=%s groupID=%s value=%s policy=%d",
			vd.AggType, aggID, vd.GroupType, groupID, optString(opt), vd.Contribution)
		rationale := "one link created: add the instance's current value to exactly one group aggregate"
		return e.commit(vd, vs, "addToGroup", input, rationale, []string{groupID}, func() {
			added, _ := e.store.AddLink(vd.LinkType, aggID, groupID)
			if !added {
				panic(txnPanic{})
			}
			m.attach(groupID, val, opt.Present)
			vs.applyContribution(groupID, val, opt.Present, +1)
		}), nil
	})
}

// RemoveFromGroup deletes one membership link.
func (e *Engine) RemoveFromGroup(view, aggID, groupID string) (OpResult, error) {
	return e.runUnit("removeFromGroup", func() (OpResult, error) {
		vd, vs, err := e.viewLocked(view)
		if err != nil {
			return OpResult{}, err
		}
		if err := e.requireGroupAndMember(vd, "removeFromGroup", groupID, aggID); err != nil {
			return OpResult{}, err
		}
		m := vs.ensureMember(aggID)
		if !contains(m.groups, groupID) {
			return OpResult{}, classified(ClassInvalid, "removeFromGroup", "no such membership")
		}
		val, pres := m.contrib[groupID], m.present[groupID]
		input := fmt.Sprintf("aggType=%s aggID=%s groupID=%s lastContribution=%v counted=%v",
			vd.AggType, aggID, groupID, val, pres)
		rationale := "one link deleted: subtract the cached last-known contribution from exactly one group"
		return e.commit(vd, vs, "removeFromGroup", input, rationale, []string{groupID}, func() {
			removed, _ := e.store.RemoveLink(vd.LinkType, aggID, groupID)
			if !removed {
				panic(txnPanic{})
			}
			m.detach(groupID)
			vs.applyContribution(groupID, val, pres, -1)
		}), nil
	})
}

// Reparent atomically moves an instance from its single current group to
// toGroup. expectedVersion is the membership-set token observed by the caller;
// a stale token makes the move a rejected ClassConcurrentConflict that changes
// nothing. Exactly two aggregate results are touched.
func (e *Engine) Reparent(view, aggID, toGroup string, expectedVersion int64) (OpResult, error) {
	return e.runUnit("reparent", func() (OpResult, error) {
		vd, vs, err := e.viewLocked(view)
		if err != nil {
			return OpResult{}, err
		}
		if err := e.requireGroupAndMember(vd, "reparent", toGroup, aggID); err != nil {
			return OpResult{}, err
		}
		current := e.store.MembersVersion(vd.LinkType, aggID)
		if current != expectedVersion {
			return OpResult{}, classified(ClassConcurrentConflict, "reparent",
				fmt.Sprintf("membership version conflict: expected %d, current %d", expectedVersion, current))
		}
		m := vs.ensureMember(aggID)
		if len(m.groups) > 1 {
			return OpResult{}, classified(ClassInvalid, "reparent",
				"reparent requires a single current membership; use remove/add for multi-group views")
		}
		oldGroup := ""
		if len(m.groups) == 1 {
			oldGroup = m.groups[0]
		}
		if oldGroup == toGroup {
			return OpResult{}, classified(ClassInvalid, "reparent", "already a member of target group")
		}
		opt := e.valueOf(vd, aggID)
		val, _ := participation(opt)
		touched := []string{toGroup}
		if oldGroup != "" {
			touched = append(touched, oldGroup)
		}
		input := fmt.Sprintf("aggID=%s oldGroup=%s newGroup=%s expectedVersion=%d value=%s",
			aggID, dash(oldGroup), toGroup, expectedVersion, optString(opt))
		rationale := "atomic move: subtract cached contribution from old group and add it to new group in one unit (exactly 2 aggregate documents)"
		return e.commit(vd, vs, "reparent", input, rationale, touched, func() {
			if oldGroup != "" {
				oldVal, oldPres := m.contrib[oldGroup], m.present[oldGroup]
				if removed, _ := e.store.RemoveLink(vd.LinkType, aggID, oldGroup); !removed {
					panic(txnPanic{})
				}
				m.detach(oldGroup)
				vs.applyContribution(oldGroup, oldVal, oldPres, -1)
			}
			added, _ := e.store.AddLink(vd.LinkType, aggID, toGroup)
			if !added {
				panic(txnPanic{})
			}
			m.attach(toGroup, val, opt.Present)
			vs.applyContribution(toGroup, val, opt.Present, +1)
		}), nil
	})
}

// DeleteGroup deletes the grouping instance. Every aggregated instance becomes
// a member of no group; the instances themselves are untouched.
func (e *Engine) DeleteGroup(view, groupID string) (OpResult, error) {
	return e.runUnit("deleteGroup", func() (OpResult, error) {
		vd, vs, err := e.viewLocked(view)
		if err != nil {
			return OpResult{}, err
		}
		if !e.store.ObjectExists(vd.GroupType, groupID) {
			return OpResult{}, classified(ClassGroupMissing, "deleteGroup",
				fmt.Sprintf("group instance %q of type %q does not exist", groupID, vd.GroupType))
		}
		input := fmt.Sprintf("groupType=%s groupID=%s", vd.GroupType, groupID)
		rationale := "group deleted: detach every member's contribution; aggregated instances are not deleted"
		return e.commit(vd, vs, "deleteGroup", input, rationale, []string{groupID}, func() {
			aggs := e.store.RemoveAllLinksToGroup(vd.LinkType, groupID)
			for _, aggID := range aggs {
				if m := vs.member[aggID]; m != nil {
					m.detach(groupID)
				}
			}
			delete(vs.group, groupID)
			if !e.store.DeleteObject(vd.GroupType, groupID) {
				panic(txnPanic{})
			}
		}), nil
	})
}

// DeleteMember deletes an aggregated instance, subtracting its last-known
// contribution from every current group within the same processing unit.
func (e *Engine) DeleteMember(view, aggID string) (OpResult, error) {
	return e.runUnit("deleteMember", func() (OpResult, error) {
		vd, vs, err := e.viewLocked(view)
		if err != nil {
			return OpResult{}, err
		}
		if !e.store.ObjectExists(vd.AggType, aggID) {
			return OpResult{}, classified(ClassTypeNotParticipating, "deleteMember",
				fmt.Sprintf("instance %q is not of aggregated type %q declared by view %q", aggID, vd.AggType, vd.Name))
		}
		m := vs.ensureMember(aggID)
		touched := append([]string(nil), m.groups...)
		input := fmt.Sprintf("aggType=%s aggID=%s memberships=%v", vd.AggType, aggID, touched)
		rationale := "member deleted: subtract cached last-known contribution from each current group, then remove the object"
		return e.commit(vd, vs, "deleteMember", input, rationale, touched, func() {
			groups := e.store.RemoveAllLinksOfAgg(vd.LinkType, aggID)
			for _, g := range groups {
				val, pres := m.contrib[g], m.present[g]
				vs.applyContribution(g, val, pres, -1)
			}
			delete(vs.member, aggID)
			if !e.store.DeleteObject(vd.AggType, aggID) {
				panic(txnPanic{})
			}
		}), nil
	})
}

// Query returns the maintained aggregate for a group.
func (e *Engine) Query(view, groupID string) (Aggregate, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	vd, vs, err := e.viewLocked(view)
	if err != nil {
		return Aggregate{}, err
	}
	if !e.store.ObjectExists(vd.GroupType, groupID) {
		return Aggregate{}, classified(ClassGroupMissing, "query",
			fmt.Sprintf("group instance %q of type %q does not exist", groupID, vd.GroupType))
	}
	g := vs.group[groupID]
	if g == nil {
		return Aggregate{}, nil
	}
	return Aggregate{Sum: g.sum, Count: g.count}, nil
}

// MembersVersion exposes the optimistic-concurrency token for one instance.
func (e *Engine) MembersVersion(view, aggID string) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	vd, _, err := e.viewLocked(view)
	if err != nil {
		return 0, err
	}
	return e.store.MembersVersion(vd.LinkType, aggID), nil
}

// Events returns a copy of the committed change log.
func (e *Engine) Events() []ChangeEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]ChangeEvent, len(e.events))
	copy(out, e.events)
	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func optString(o Optional) string {
	if !o.Present {
		return "absent"
	}
	return fmt.Sprintf("%v", o.Value)
}
