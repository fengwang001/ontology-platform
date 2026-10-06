package rider

import "sort"

type appealState struct {
	id      int64
	eventID int64
	riderID string
	upheld  bool
	decided bool
}

// submitAppeal 提交申诉（调用方已完成参数/时钟/骑手/事件存在性校验与结算推进）。
func (s *System) submitAppeal(rs *riderState, now int64, riderID string, eventID int64) (int64, error) {
	e := s.events[eventID]
	if e.Revoked {
		return 0, newError(ErrEventRevoked, "event already revoked")
	}
	if e.AppealID != 0 {
		return 0, newError(ErrAlreadyAppealed, "event already appealed")
	}
	if now < e.Time || now >= e.Time+s.cfg.AppealWindow {
		return 0, newError(ErrAppealWindowExpired, "outside appeal window")
	}
	// 连带事件：存活但不是其簇的计扣分（最早）事件。
	if e.RootKey != "" {
		node := treapFind(rs.clusters.trees[e.RootKey], cKey{t: e.Time, seq: e.Seq})
		if node == nil || rs.clusters.dsu[node].head != node {
			return 0, newError(ErrCompanionEvent, "companion event cannot be appealed alone")
		}
	}
	id := s.nextAppealID
	s.nextAppealID++
	a := &appealState{id: id, eventID: eventID, riderID: riderID}
	s.appeals[id] = a
	e.AppealID = id
	s.advance(now)
	return id, nil
}

// ruleAppeal 裁决申诉；成立则整簇撤销并按需回溯补偿。
func (s *System) ruleAppeal(rs *riderState, now int64, riderID string, appealID int64, upheld bool) (*Compensation, error) {
	a := s.appeals[appealID]
	if a.decided {
		return nil, newError(ErrAppealDecided, "appeal already ruled")
	}
	if s.events[a.eventID].Revoked {
		return nil, newError(ErrEventRevoked, "event already revoked")
	}
	a.decided = true
	a.upheld = upheld
	s.advance(now)
	if !upheld {
		// 驳回不改任何事件/簇/结算状态，仅记录裁决。
		return nil, nil
	}

	e := s.events[a.eventID]
	ids := rs.clusters.revokeCluster(s, rs, e)

	// 1) 更新周期账目：撤销所有计扣分贡献。
	affectedSet := map[int]bool{}
	for _, id := range ids {
		ev := s.events[id]
		ev.Revoked = true
		p := s.periodOf(ev.Time)
		ps := rs.periods[p]
		if ps != nil {
			if score, ok := ps.counting[id]; ok {
				delete(ps.counting, id)
				ps.liveScore -= score
			}
			if ps.settled {
				if _, frozen := ps.frozenContrib[id]; frozen {
					delete(ps.frozenContrib, id)
					affectedSet[p] = true
				}
			}
		}
	}

	// 2) 回溯：对受影响的已结算周期，按序重算“本应等级/权益”。
	affected := make([]int, 0, len(affectedSet))
	for p := range affectedSet {
		affected = append(affected, p)
	}
	sort.Ints(affected)

	totalDiff := 0
	var lastShadowRights int
	lastAffected := rs.nextSettle - 1
	for _, p := range affected {
		ps := rs.periods[p]
		wouldScore := 0
		for _, score := range ps.frozenContrib {
			wouldScore += score
		}
		wouldLevel := s.levelOf(wouldScore)
		baseline := ps.baseline
		if p > 0 {
			if prev, ok := rs.periods[p-1]; ok {
				baseline = prev.rights
			}
		}
		shadowRights := wouldLevel
		if capLevel := baseline + s.cfg.MaxLevelDrop; capLevel < shadowRights {
			shadowRights = capLevel
		}
		if diff := ps.rights - shadowRights; diff > 0 {
			totalDiff += diff
		}
		lastShadowRights = shadowRights
		lastAffected = p
		_ = lastAffected
	}

	var comp *Compensation
	if totalDiff > 0 {
		comp = &Compensation{
			AppealID: appealID,
			RiderID:  riderID,
			Amount:   totalDiff * s.cfg.CompensationPerLevel,
		}
		s.comps = append(s.comps, *comp)
	}

	// 3) 改写下降上限基准：作用于裁决时尚未开始的第一个周期；
	//    基准沿已结算周期之间的空档按本应权益传播，保证可复现。
	if len(affected) > 0 {
		start := s.periodOf(now)
		if s.periodRight(start) <= now {
			start++
		}
		// 先把本应权益传播到最后一个已结算周期。
		propRights := lastShadowRights
		for p := affected[len(affected)-1] + 1; p < start && p < rs.nextSettle; p++ {
			ps := rs.period(p)
			level := ps.level
			shadow := level
			if capLevel := propRights + s.cfg.MaxLevelDrop; capLevel < shadow {
				shadow = capLevel
			}
			propRights = shadow
		}
		if cur, ok := rs.overrides[start]; !ok || propRights < cur {
			rs.overrides[start] = propRights
		}
	}

	return comp, nil
}
