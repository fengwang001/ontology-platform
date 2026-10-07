package retention

import "sort"

// Query 判定某身份对某对象的可见性。第二个返回值表示对象是否存在
// （存在性对两种身份一致；一般使用者对非存活对象得到“视为不存在”）。
//
// 查询也是“涉及该对象的操作”，因此同样承担宽限到期的惰性归档：
// 一次 Query 在锁内先完成到期提升再读取，期间任何其他操作都不能插入，
// 因此不可能观察到两种状态之间的中间形态。
func (s *Service) Query(id string, role Role) (Visibility, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	o, ok := s.objects[id]
	if !ok {
		s.log(AuditEntry{At: now, Op: "query", ObjectID: id, Success: true,
			Input:  map[string]any{"role": role.String()},
			Output: map[string]any{"exists": false, "visible": false}})
		return Visibility{Exists: false}, false
	}
	s.promoteLocked(id, o, now)
	o.probes++
	switch role {
	case RoleUser:
		if o.state != StateAlive {
			// 非存活对象对一般使用者一律视为不存在：
			// 不泄露状态与截止时刻。
			s.log(AuditEntry{At: now, Op: "query", ObjectID: id, Success: true,
				StateBefore: o.state, StateAfter: o.state,
				Input:  map[string]any{"role": role.String()},
				Output: map[string]any{"exists": true, "visible": false, "state": o.state.String()}})
			return Visibility{Visible: false}, true
		}
		v := Visibility{Visible: true, Exists: true, State: StateAlive,
			Attributes: copyAttrs(o.attrs)}
		s.log(AuditEntry{At: now, Op: "query", ObjectID: id, Success: true,
			StateBefore: StateAlive, StateAfter: StateAlive,
			Input:  map[string]any{"role": role.String()},
			Output: map[string]any{"exists": true, "visible": true, "state": "ALIVE"}})
		return v, true
	default: // RoleAdmin
		v := Visibility{Visible: true, Exists: true, State: o.state,
			GraceDeadline:  o.graceDeadline,
			FreezeDeadline: o.freezeDeadline}
		if o.state != StateFrozen {
			// 冻结对象只能看到状态本身与冻结截止时刻，业务属性不返回。
			v.Attributes = copyAttrs(o.attrs)
		}
		s.log(AuditEntry{At: now, Op: "query", ObjectID: id, Success: true,
			StateBefore: o.state, StateAfter: o.state,
			Input: map[string]any{"role": role.String()},
			Output: map[string]any{
				"exists":          true,
				"visible":         true,
				"state":           o.state.String(),
				"attrs_redacted":  o.state == StateFrozen,
				"grace_deadline":  o.graceDeadline,
				"freeze_deadline": o.freezeDeadline,
			}})
		return v, true
	}
}

// EdgeVisible 判定单条出边对某身份是否可见。
// 出边可见性完全跟随源对象：源不可见 => 出边一律不可见，
// 即使链接本身没有任何删除标记。目标对象的状态不影响出边可见性。
func (s *Service) EdgeVisible(sourceID, targetID string, role Role) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	if o, ok := s.objects[sourceID]; ok {
		s.promoteLocked(sourceID, o, now)
	}
	if _, ok := s.edges[edge{sourceID, targetID}]; !ok {
		return false
	}
	o, ok := s.objects[sourceID]
	if !ok {
		return false
	}
	o.probes++
	return sourceVisibleLocked(o, role)
}

// VisibleEdges 返回源对象对该身份可见的全部出边目标（排序，便于比对）。
// 与对象可见性在同一把读锁内判定，保证同一次查询内两者结论一致。
func (s *Service) VisibleEdges(sourceID string, role Role) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	o, ok := s.objects[sourceID]
	if !ok {
		return nil
	}
	s.promoteLocked(sourceID, o, now)
	if !sourceVisibleLocked(o, role) {
		return nil
	}
	o.probes++
	targets := make([]string, 0, len(s.outOf[sourceID]))
	for t := range s.outOf[sourceID] {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	return targets
}

// QueryWithEdges 在同一个线性化点内返回对象可见性与其全部出边，
// 保证同一次查询不会对对象和出边得出不一致的结论。
func (s *Service) QueryWithEdges(id string, role Role) (Visibility, []string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	o, ok := s.objects[id]
	if !ok {
		return Visibility{Exists: false}, nil, false
	}
	s.promoteLocked(id, o, now)
	o.probes++
	v := Visibility{Exists: true, State: o.state,
		GraceDeadline: o.graceDeadline, FreezeDeadline: o.freezeDeadline}
	var edges []string
	switch role {
	case RoleUser:
		if o.state != StateAlive {
			return Visibility{Visible: false}, nil, true
		}
		v.Visible = true
		v.Attributes = copyAttrs(o.attrs)
	default:
		v.Visible = true
		if o.state != StateFrozen {
			v.Attributes = copyAttrs(o.attrs)
		}
	}
	if sourceVisibleLocked(o, role) {
		for t := range s.outOf[id] {
			edges = append(edges, t)
		}
		sort.Strings(edges)
	}
	return v, edges, true
}

// sourceVisibleLocked 是出边跟随源对象可见性的唯一判定点。
// 注意：管理员在 FROZEN 状态下虽能看到状态与冻结截止时刻，
// 但出边链接属于业务关系数据，随业务属性一起遮蔽
// （设计取舍见 DESIGN.md）。
func sourceVisibleLocked(o *object, role Role) bool {
	switch role {
	case RoleAdmin:
		return o.state == StateAlive || o.state == StateGrace || o.state == StateArchived
	default:
		return o.state == StateAlive
	}
}

// State 返回对象当前状态快照（含截止时刻），供测试与朴素模型对照使用。
// 该读取不触发宽限到期提升（纯查询不改写状态）。
func (s *Service) State(id string) (State, int64, int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objects[id]
	if !ok {
		return 0, 0, 0, false
	}
	return o.state, o.graceDeadline, o.freezeDeadline, true
}

// ProbeCount 返回对象状态字段被判定逻辑读取的累计次数，仅供测试验证
// O(1) 历史记录复杂度。
func (s *Service) ProbeCount(id string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if o, ok := s.objects[id]; ok {
		return o.probes
	}
	return 0
}

// EdgeKnown 仅判断出边是否已登记，不含任何可见性判定，供测试使用。
func (s *Service) EdgeKnown(sourceID, targetID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.edges[edge{sourceID, targetID}]
	return ok
}
