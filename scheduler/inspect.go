package scheduler

// ReplicaStatus 描述副本当前的生命周期状态。
type ReplicaStatus struct {
	ReplicaID string
	NodeID    string
	Zone      string
	// State 为 reserved / binding / bound / released。
	State string
}

// UsedSlots 返回节点当前已占用槽位（已绑定 + 已预留）。
func (s *Scheduler) UsedSlots(nodeID string) (int, bool) {
	impl := s.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	n := impl.core.nodes[nodeID]
	if n == nil {
		return 0, false
	}
	return n.used, true
}

// ZoneCounts 返回组在每个合格区内的计数（已绑定 + 已预留，已释放不计）。
func (s *Scheduler) ZoneCounts(groupID string) (map[string]int, error) {
	impl := s.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	group := impl.core.groups[groupID]
	if group == nil {
		return nil, newErr("inspect", ReasonUnknownGroup, "unknown group "+groupID)
	}
	zones := impl.evalZones(group)
	counts := make(map[string]int, len(zones))
	for name, z := range zones {
		counts[name] = z.count
	}
	return counts, nil
}

// Replicas 返回组内全部副本的当前状态快照。
func (s *Scheduler) Replicas(groupID string) (map[string]ReplicaStatus, error) {
	impl := s.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()
	if _, ok := impl.core.groups[groupID]; !ok {
		return nil, newErr("inspect", ReasonUnknownGroup, "unknown group "+groupID)
	}
	out := make(map[string]ReplicaStatus)
	for id, rep := range impl.core.replicas[groupID] {
		out[id] = ReplicaStatus{
			ReplicaID: id,
			NodeID:    rep.node,
			Zone:      rep.zone,
			State:     string(rep.state),
		}
	}
	return out, nil
}
