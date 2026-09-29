package scheduler

import (
	"log/slog"
	"maps"
	"slices"
	"sync"
)

// Placement 描述一次成功调度的放置结果。
type Placement struct {
	GroupID   string
	ReplicaID string
	NodeID    string
	Zone      string
}

// schedulerCore 承载受互斥锁保护的全部可变状态。
type schedulerCore struct {
	nodes  map[string]*nodeState
	groups map[string]*groupState
	// replicas 以 groupID -> replicaID 索引。
	replicas map[string]map[string]*replica
}

// schedulerImpl 是 Scheduler 的内部实现。
type schedulerImpl struct {
	mu     sync.Mutex
	core   schedulerCore
	binder Binder
	logger *slog.Logger
}

// New 创建调度器并注册节点。
// 节点级校验失败（槽位非正、标识重复、区为空）时整体拒绝，不保留任何注册状态。
func New(cfg Config, nodes []Node) (*Scheduler, error) {
	registered := make(map[string]*nodeState, len(nodes))
	for _, n := range nodes {
		if n.Slots <= 0 {
			return nil, newErr("new", ReasonInvalidSlots,
				"node "+n.ID+" has non-positive slots")
		}
		if n.Zone == "" {
			return nil, newErr("new", ReasonEmptyZone,
				"node "+n.ID+" has empty zone")
		}
		if _, dup := registered[n.ID]; dup {
			return nil, newErr("new", ReasonDuplicateNode,
				"node id "+n.ID+" registered more than once")
		}
		registered[n.ID] = &nodeState{
			id:     n.ID,
			zone:   n.Zone,
			slots:  n.Slots,
			labels: maps.Clone(n.Labels),
		}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	impl := &schedulerImpl{
		core: schedulerCore{
			nodes:    registered,
			groups:   make(map[string]*groupState),
			replicas: make(map[string]map[string]*replica),
		},
		binder: cfg.Binder,
		logger: logger,
	}
	logger.Info("scheduler created", "nodes", slices.Sorted(maps.Keys(registered)))
	return &Scheduler{impl: impl}, nil
}

// AddGroup 注册一个副本组；标签要求无节点满足时整体拒绝。
func (s *Scheduler) AddGroup(g Group) error {
	impl := s.impl
	if g.Skew < 1 {
		return newErr("add_group", ReasonInvalidSkew,
			"group "+g.ID+" skew must be >= 1")
	}
	impl.mu.Lock()
	defer impl.mu.Unlock()
	if _, exists := impl.core.groups[g.ID]; exists {
		return newErr("add_group", ReasonDuplicateNode,
			"group "+g.ID+" already exists")
	}
	matched := false
	for _, n := range impl.core.nodes {
		if nodeMatches(n, g.RequiredLabels) {
			matched = true
			break
		}
	}
	if !matched {
		return newErr("add_group", ReasonNoMatchingNode,
			"no node satisfies required labels of group "+g.ID)
	}
	impl.core.groups[g.ID] = &groupState{
		id:             g.ID,
		skew:           g.Skew,
		requiredLabels: maps.Clone(g.RequiredLabels),
	}
	impl.core.replicas[g.ID] = make(map[string]*replica)
	impl.logger.Info("group added",
		"group", g.ID, "skew", g.Skew, "required_labels", g.RequiredLabels)
	return nil
}

// Schedule 为组内副本选择节点并立即预留槽位。
func (s *Scheduler) Schedule(groupID, replicaID string) (Placement, error) {
	impl := s.impl
	impl.mu.Lock()
	defer impl.mu.Unlock()

	group := impl.core.groups[groupID]
	if group == nil {
		err := newErr("schedule", ReasonUnknownGroup, "unknown group "+groupID)
		impl.logger.Warn("schedule rejected",
			"group", groupID, "replica", replicaID, "reason", err.Reason)
		return Placement{}, err
	}
	reps := impl.core.replicas[groupID]
	if existing, dup := reps[replicaID]; dup {
		err := newErr("schedule", ReasonDuplicateReplica,
			"replica "+replicaID+" already exists on node "+existing.node+
				" in state "+string(existing.state))
		impl.logger.Warn("schedule rejected",
			"group", groupID, "replica", replicaID, "reason", err.Reason)
		return Placement{}, err
	}

	zones := impl.evalZones(group)
	zone, reason := chooseZone(zones, group.skew)
	if reason != "" {
		err := newErr("schedule", reason,
			"no placeable zone for replica "+replicaID)
		counts := make(map[string]int, len(zones))
		for name, z := range zones {
			counts[name] = z.count
		}
		impl.logger.Warn("schedule rejected",
			"group", groupID, "replica", replicaID, "reason", reason,
			"skew", group.skew, "zone_counts", counts)
		return Placement{}, err
	}

	node := zone.freeNode
	node.used++
	rep := &replica{
		id:    replicaID,
		group: groupID,
		node:  node.id,
		zone:  zone.zone,
		state: stateReserved,
	}
	reps[replicaID] = rep

	placement := Placement{
		GroupID:   groupID,
		ReplicaID: replicaID,
		NodeID:    node.id,
		Zone:      zone.zone,
	}
	impl.logger.Info("schedule reserved",
		"input_group", groupID, "input_replica", replicaID,
		"output", placement,
		"basis_zone", zone.zone,
		"basis_zone_count", zone.count+1,
		"basis_min_count", zone.count,
		"basis_skew", group.skew,
		"basis_node", node.id,
		"basis_free_slots", node.slots-node.used,
		"basis_required", group.requiredLabels)
	return placement, nil
}

// Bind 对已预留的副本执行异步绑定；失败时释放预留。
func (s *Scheduler) Bind(groupID, replicaID string) error {
	impl := s.impl

	impl.mu.Lock()
	rep := impl.core.replicas[groupID][replicaID]
	if rep == nil {
		impl.mu.Unlock()
		err := newErr("bind", ReasonUnknownReservation,
			"no reservation for replica "+replicaID)
		impl.logger.Warn("bind rejected",
			"group", groupID, "replica", replicaID, "reason", err.Reason)
		return err
	}
	impl.mu.Unlock()

	// 同一预留的绑定串行化；与删除互斥，保证生命周期状态迁移确定。
	rep.bindMu.Lock()
	defer rep.bindMu.Unlock()

	impl.mu.Lock()
	current := impl.core.replicas[groupID][replicaID]
	if current != rep {
		impl.mu.Unlock()
		return newErr("bind", ReasonUnknownReservation,
			"reservation for replica "+replicaID+" changed")
	}
	switch rep.state {
	case stateBound:
		impl.mu.Unlock()
		err := newErr("bind", ReasonAlreadyBound,
			"replica "+replicaID+" already bound")
		impl.logger.Warn("bind rejected",
			"group", groupID, "replica", replicaID, "reason", err.Reason)
		return err
	case stateReleased:
		impl.mu.Unlock()
		err := newErr("bind", ReasonAlreadyReleased,
			"replica "+replicaID+" already released")
		impl.logger.Warn("bind rejected",
			"group", groupID, "replica", replicaID, "reason", err.Reason)
		return err
	case stateBinding:
		impl.mu.Unlock()
		err := newErr("bind", ReasonAlreadyBound,
			"bind for replica "+replicaID+" already in flight")
		impl.logger.Warn("bind rejected",
			"group", groupID, "replica", replicaID, "reason", err.Reason)
		return err
	}
	node := impl.core.nodes[rep.node]
	rep.state = stateBinding
	binder := impl.binder
	impl.mu.Unlock()

	// 绑定函数在全局锁外执行，避免拖慢并发调度；槽位在结果落定前保持预留。
	var bindErr error
	if binder != nil {
		bindErr = binder(groupID, replicaID, node.id)
	}

	impl.mu.Lock()
	defer impl.mu.Unlock()
	if bindErr != nil {
		// 绑定失败：释放预留，计数与槽位一并回退。
		if rep.state == stateBinding {
			rep.state = stateReleased
			if node.used > 0 {
				node.used--
			}
		}
		impl.logger.Warn("bind failed and reservation released",
			"input", map[string]string{
				"group": groupID, "replica": replicaID, "node": node.id,
			},
			"error", bindErr.Error())
		return bindErr
	}
	if rep.state == stateBinding {
		rep.state = stateBound
	}
	impl.logger.Info("bind succeeded",
		"input", map[string]string{
			"group": groupID, "replica": replicaID, "node": node.id,
		},
		"output", "bound")
	return nil
}

// DeleteReplica 删除副本并释放计数；不触发任何重新平衡。
func (s *Scheduler) DeleteReplica(groupID, replicaID string) error {
	impl := s.impl

	impl.mu.Lock()
	rep := impl.core.replicas[groupID][replicaID]
	if rep == nil {
		if _, ok := impl.core.groups[groupID]; !ok {
			impl.mu.Unlock()
			err := newErr("delete", ReasonUnknownGroup, "unknown group "+groupID)
			impl.logger.Warn("delete rejected",
				"group", groupID, "replica", replicaID, "reason", err.Reason)
			return err
		}
		impl.mu.Unlock()
		err := newErr("delete", ReasonUnknownReplica,
			"replica "+replicaID+" does not exist")
		impl.logger.Warn("delete rejected",
			"group", groupID, "replica", replicaID, "reason", err.Reason)
		return err
	}
	node := impl.core.nodes[rep.node]
	impl.mu.Unlock()

	// 与在途绑定互斥：删除要么先于绑定（绑定随后发现副本消失），
	// 要么后于绑定，两者都是确定的串行顺序。
	rep.bindMu.Lock()
	defer rep.bindMu.Unlock()

	impl.mu.Lock()
	current := impl.core.replicas[groupID][replicaID]
	if current != rep {
		impl.mu.Unlock()
		return newErr("delete", ReasonUnknownReplica,
			"replica "+replicaID+" vanished during delete")
	}
	hadCount := rep.state != stateReleased
	delete(impl.core.replicas[groupID], replicaID)
	if hadCount && node.used > 0 {
		node.used--
	}
	freeSlots := node.slots - node.used
	impl.mu.Unlock()
	impl.logger.Info("replica deleted",
		"input", map[string]string{
			"group": groupID, "replica": replicaID, "node": node.id,
		},
		"output", "deleted",
		"basis", map[string]any{
			"had_count":  hadCount,
			"rebalance":  false,
			"free_slots": freeSlots,
		})
	return nil
}
