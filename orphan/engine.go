package orphan

import "sort"

// Determine 对单个对象做孤儿追溯判定。
// 判定是关于 (事件流快照, Query.At, Query.Version) 的纯函数：
// 在锁内取一致快照后计算，重复调用结果完全一致（幂等）。
// 判定失败时不会对事件流或规则账本产生任何可观察改动。
func (s *Store) Determine(q Query) (Determination, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec := AuditRecord{Seq: s.opSeq, Query: q, Declared: q.Version, Governing: -1}
	det, err := s.determineLocked(q, &rec)
	s.recordAudit(rec)
	return det, err
}

func (s *Store) determineLocked(q Query, rec *AuditRecord) (Determination, error) {
	det := Determination{Query: q}

	// 优先级 1：规则版本作废。
	gov, ok := GoverningVersion(s.ledger, q.At)
	if ok {
		rec.Governing = gov.Version
	}
	if !ok || gov.Version != q.Version {
		rec.Err = ErrRuleVersionVoided
		return det, &Error{Code: ErrRuleVersionVoided, Detail: "declared rule version is not governing the requested time"}
	}
	det.Version = gov.Version

	st, ok := s.objects[q.Object]
	if !ok || !st.created {
		rec.Err = ErrDanglingReference
		return det, &Error{Code: ErrDanglingReference, Detail: "object has not been created"}
	}

	slice := sliceOf(st.events, q.At)
	rec.EventsScanned = len(st.events)
	det.EventsScanned = len(st.events)

	// 优先级 2：并列记录顺序不可确定。
	if bad := ambiguousGroup(slice); bad != nil {
		rec.Err = ErrAmbiguousOrder
		return det, &Error{Code: ErrAmbiguousOrder, Detail: "concurrent events at the same time cannot be ordered: " + string(bad.ID)}
	}

	// 优先级 3：悬置引用。
	if err := s.checkRefsLocked(slice); err != nil {
		rec.Err = err.Code
		return det, err
	}

	// 优先级 4：请求时刻早于对象首次出现。
	if q.At < st.createdAt {
		rec.Err = ErrTimeBeforeFirstAppearance
		return det, &Error{Code: ErrTimeBeforeFirstAppearance, Detail: "requested time is before the object's first appearance"}
	}

	det = evaluate(q, gov.Spec, st.objType, slice)
	det.Version = gov.Version
	det.EventsScanned = len(st.events)
	rec.Status = det.Status
	return det, nil
}

// sliceOf 过滤出 Time <= t 的相关事件并按 (Time, Seq) 排序。
func sliceOf(events []Event, t Time) []Event {
	out := make([]Event, 0, len(events))
	for _, e := range events {
		if e.Time <= t {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

// eventEntities 返回事件触及的实体集合；同刻事件只有触及共同实体时
// 其相对顺序才影响重建结果（不同实体的事件可交换）。
func eventEntities(e Event) []string {
	switch e.Kind {
	case EvLinkTypeCreated:
		return []string{"lt:" + string(e.LinkType)}
	case EvObjectCreated, EvPropertySet, EvObjectMarkedOrphan:
		return []string{"o:" + string(e.Object)}
	case EvLinkCreated:
		return []string{"l:" + string(e.Link), "o:" + string(e.From), "o:" + string(e.To)}
	case EvLinkRevoked:
		return []string{"l:" + string(e.Link)}
	}
	return nil
}

func entitiesConflict(a, b Event) bool {
	set := make(map[string]bool)
	for _, s := range eventEntities(a) {
		set[s] = true
	}
	for _, s := range eventEntities(b) {
		if set[s] {
			return true
		}
	}
	return false
}

// ambiguousGroup 检查同一时刻的并列事件：凡触及共同实体的事件对，
// 必须能通过组内 After 链确定先后（传递闭包），否则顺序不可确定。
// 返回冲突证据事件；无歧义时返回 nil。
func ambiguousGroup(slice []Event) *Event {
	for i := 0; i < len(slice); {
		j := i
		for j < len(slice) && slice[j].Time == slice[i].Time {
			j++
		}
		if j-i > 1 {
			if bad := unorderedConflict(slice[i:j]); bad != nil {
				return bad
			}
		}
		i = j
	}
	return nil
}

// unorderedConflict 在同刻事件组内寻找"触及共同实体但因果无序"的事件对。
func unorderedConflict(group []Event) *Event {
	// 组内 After 关系的传递闭包。
	reach := make(map[EventID]map[EventID]bool, len(group))
	inGroup := make(map[EventID]bool, len(group))
	for _, e := range group {
		inGroup[e.ID] = true
	}
	for _, e := range group {
		if inGroup[e.After] {
			if reach[e.ID] == nil {
				reach[e.ID] = map[EventID]bool{}
			}
			reach[e.ID][e.After] = true
		}
	}
	// Floyd 传递闭包（组通常很小）。
	for _, k := range group {
		for _, a := range group {
			if !reach[a.ID][k.ID] {
				continue
			}
			for b := range reach[k.ID] {
				if reach[a.ID] == nil {
					reach[a.ID] = map[EventID]bool{}
				}
				reach[a.ID][b] = true
			}
		}
	}
	for x := 0; x < len(group); x++ {
		for y := x + 1; y < len(group); y++ {
			a, b := group[x], group[y]
			if reach[a.ID][b.ID] || reach[b.ID][a.ID] {
				continue
			}
			if entitiesConflict(a, b) {
				return &group[x]
			}
		}
	}
	return nil
}

// checkRefsLocked 校验切片内引用合法性（调用方须持锁）。
func (s *Store) checkRefsLocked(slice []Event) *Error {
	for _, e := range slice {
		switch e.Kind {
		case EvPropertySet, EvObjectMarkedOrphan:
			if ost := s.objects[e.Object]; ost == nil || !ost.created || ost.createdAt > e.Time {
				return &Error{Code: ErrDanglingReference, Detail: "event references object before its creation"}
			}
		case EvLinkCreated:
			lt, ok := s.linkTypes[e.LinkType]
			if !ok || lt > e.Time {
				return &Error{Code: ErrDanglingReference, Detail: "link created with unknown link type"}
			}
			for _, ep := range []ObjectID{e.From, e.To} {
				ost := s.objects[ep]
				if ost == nil || !ost.created || ost.createdAt > e.Time {
					return &Error{Code: ErrDanglingReference, Detail: "link endpoint not yet created"}
				}
			}
		case EvLinkRevoked:
			m, ok := s.links[e.Link]
			if !ok || m.createdAt > e.Time {
				return &Error{Code: ErrDanglingReference, Detail: "revoked link not yet created"}
			}
		}
	}
	return nil
}

// evaluate 是孤儿判定的核心：在对象相关事件切片上按规则版本重放。
func evaluate(q Query, spec RuleSpec, objType ObjectTypeID, slice []Event) Determination {
	det := Determination{Query: q, Status: StatusNotOrphan}

	active := make(map[LinkID]bool)
	everHad := false
	zeroSince := Time(-1)
	var marks []Time

	for _, e := range slice {
		switch e.Kind {
		case EvLinkCreated:
			if e.From == q.Object && spec.Requires(objType, e.LinkType) {
				active[e.Link] = true
				everHad = true
				zeroSince = -1
			}
		case EvLinkRevoked:
			if active[e.Link] {
				delete(active, e.Link)
			}
			if everHad && len(active) == 0 && zeroSince < 0 {
				zeroSince = e.Time
			}
		case EvObjectMarkedOrphan:
			marks = append(marks, e.Time)
		}
	}

	if !everHad || len(active) != 0 || zeroSince < 0 {
		return det
	}
	det.ZeroSince = zeroSince
	det.OrphanDue = zeroSince + spec.GracePeriod
	if det.OrphanDue > q.At {
		return det
	}

	confirmedAt := Time(0)
	confirmed := false
	for _, m := range marks {
		if m >= zeroSince && (!confirmed || m < confirmedAt) {
			confirmed = true
			confirmedAt = m
		}
	}
	if confirmed {
		det.Status = StatusOrphanConfirmed
		det.ConfirmedAt = confirmedAt
		return det
	}

	for _, e := range slice {
		if e.Time <= det.OrphanDue {
			continue
		}
		switch e.Kind {
		case EvPropertySet:
			det.Activities = append(det.Activities, Activity{Time: e.Time, Kind: e.Kind, Seq: e.Seq})
		case EvLinkCreated:
			det.Activities = append(det.Activities, Activity{Time: e.Time, Kind: e.Kind, Seq: e.Seq})
		}
	}
	if len(det.Activities) > 0 {
		det.Status = StatusRetroOrphanWithActivity
	} else {
		det.Status = StatusRetroOrphanDormant
	}
	return det
}

// RebuildNetwork 以纯事件溯源方式重建整个网络在时刻 t 的状态。
// 全量前缀扫描，错误检查范围为 Time <= t 的全部事件。
func (s *Store) RebuildNetwork(t Time) (NetworkState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := sliceOf(s.events, t)
	if bad := ambiguousGroup(prefix); bad != nil {
		return NetworkState{}, &Error{Code: ErrAmbiguousOrder, Detail: "concurrent events at the same time cannot be ordered: " + string(bad.ID)}
	}
	if err := s.checkRefsLocked(prefix); err != nil {
		return NetworkState{}, err
	}

	state := NetworkState{At: t, Objects: map[ObjectID]ObjectState{}, Links: map[LinkID]LinkState{}}
	for _, e := range prefix {
		switch e.Kind {
		case EvObjectCreated:
			ost, ok := state.Objects[e.Object]
			if !ok {
				ost = ObjectState{Type: e.ObjectType, CreatedAt: e.Time, Properties: map[string]string{}}
			}
			state.Objects[e.Object] = ost
		case EvPropertySet:
			ost := state.Objects[e.Object]
			if ost.Properties == nil {
				ost.Properties = map[string]string{}
			}
			ost.Properties[e.Key] = e.Value
			state.Objects[e.Object] = ost
		case EvObjectMarkedOrphan:
			ost := state.Objects[e.Object]
			ost.MarkedOrphan = true
			state.Objects[e.Object] = ost
		case EvLinkCreated:
			state.Links[e.Link] = LinkState{Type: e.LinkType, From: e.From, To: e.To, CreatedAt: e.Time, Active: true}
		case EvLinkRevoked:
			ls := state.Links[e.Link]
			ls.Active = false
			state.Links[e.Link] = ls
		}
	}
	return state, nil
}
