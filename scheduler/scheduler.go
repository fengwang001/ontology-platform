package scheduler

import (
	"context"
	"sync"
)

// Logger 是调度器使用的最小日志接口：打印输入、输出与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// Node 是带可用区标签与槽位容量的节点。
type Node struct {
	ID     string
	Zone   string
	Slots  int
	Labels map[string]string
}

// GroupSpec 声明一个副本组的允许偏斜 S 与必需的节点标签集合。
type GroupSpec struct {
	ID    string
	Skew  int
	Needs map[string]string
}

// Replica 是一次成功调度（预留）后的副本视图。
type Replica struct {
	Group string
	ID    string
	Zone  string
	Node  string
}

// BindFunc 模拟异步绑定过程，可被注入失败。
// 返回非 nil 即视为绑定失败，预留将被释放。
type BindFunc func(ctx context.Context, groupID, replicaID, nodeID string) error

// reservationState 预留生命周期。
type reservationState int

const (
	stateReserved reservationState = iota
	stateBinding
	stateBound
	stateReleased
)

type nodeState struct {
	spec Node
	used int
}

type groupState struct {
	skew      int
	needs     map[string]string
	zoneCount map[string]int
	replicas  map[string]*replicaState
}

type replicaState struct {
	id    string
	zone  string
	node  string
	state reservationState
}

// Scheduler 是按可用区均匀打散的副本调度器。
// 所有方法可被并发调用；调度在单个互斥锁下完成判定与预留，
// 因此并发调度等价于某个串行顺序。
type Scheduler struct {
	mu     sync.Mutex
	nodes  map[string]*nodeState
	groups map[string]*groupState
	log    Logger
}

// Option 配置 Scheduler。
type Option func(*Scheduler)

// WithLogger 注入日志实现；nil 表示关闭日志。
func WithLogger(l Logger) Option {
	return func(s *Scheduler) { s.log = l }
}

// New 创建调度器并登记节点。
// 槽位非正、节点标识重复或区标签为空会整体拒绝。
func New(nodes []Node, opts ...Option) (*Scheduler, error) {
	s := &Scheduler{
		nodes:  make(map[string]*nodeState),
		groups: make(map[string]*groupState),
	}
	for _, opt := range opts {
		opt(s)
	}
	for _, n := range nodes {
		if n.Slots <= 0 {
			s.logf("input New reject: node=%q slots=%d reason=%v", n.ID, n.Slots, ErrInvalidSlots)
			return nil, ErrInvalidSlots
		}
		if n.Zone == "" {
			s.logf("input New reject: node=%q zone=%q reason=%v", n.ID, n.Zone, ErrEmptyZone)
			return nil, ErrEmptyZone
		}
		if _, dup := s.nodes[n.ID]; dup {
			s.logf("input New reject: node=%q reason=%v", n.ID, ErrDuplicateNode)
			return nil, ErrDuplicateNode
		}
		labels := make(map[string]string, len(n.Labels))
		for k, v := range n.Labels {
			labels[k] = v
		}
		s.nodes[n.ID] = &nodeState{
			spec: Node{ID: n.ID, Zone: n.Zone, Slots: n.Slots, Labels: labels},
		}
	}
	s.logf("input New nodes=%d output ok", len(nodes))
	return s, nil
}

// AddGroup 登记副本组。S 小于 1 或标签要求无节点满足时拒绝。
func (s *Scheduler) AddGroup(spec GroupSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if spec.Skew < 1 {
		s.logf("input AddGroup group=%q skew=%d reason=%v", spec.ID, spec.Skew, ErrInvalidSkew)
		return ErrInvalidSkew
	}
	if _, dup := s.groups[spec.ID]; dup {
		s.logf("input AddGroup group=%q reason=%v", spec.ID, ErrDuplicateGroup)
		return ErrDuplicateGroup
	}
	matched := 0
	for _, n := range s.nodes {
		if nodeSatisfies(n.spec.Labels, spec.Needs) {
			matched++
		}
	}
	if matched == 0 {
		s.logf("input AddGroup group=%q needs=%v reason=%v", spec.ID, spec.Needs, ErrNoMatchingNode)
		return ErrNoMatchingNode
	}
	needs := make(map[string]string, len(spec.Needs))
	for k, v := range spec.Needs {
		needs[k] = v
	}
	s.groups[spec.ID] = &groupState{
		skew:      spec.Skew,
		needs:     needs,
		zoneCount: make(map[string]int),
		replicas:  make(map[string]*replicaState),
	}
	s.logf("input AddGroup group=%q skew=%d needs=%v matched-nodes=%d output ok",
		spec.ID, spec.Skew, spec.Needs, matched)
	return nil
}

// Schedule 把同组副本逐个放到带区标签的节点上：成功即预留。
func (s *Scheduler) Schedule(groupID, replicaID string) (Replica, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[groupID]
	if !ok {
		s.logf("input Schedule group=%q replica=%q reason=%v", groupID, replicaID, ErrUnknownGroup)
		return Replica{}, ErrUnknownGroup
	}
	if _, dup := g.replicas[replicaID]; dup {
		s.logf("input Schedule group=%q replica=%q reason=%v", groupID, replicaID, ErrDuplicateReplica)
		return Replica{}, ErrDuplicateReplica
	}

	// 合格区：至少有一个满足标签要求的节点（不论有无空槽）。
	eligible := make(map[string]struct{})
	for _, n := range s.nodes {
		if nodeSatisfies(n.spec.Labels, g.needs) {
			eligible[n.spec.Zone] = struct{}{}
		}
	}
	if len(eligible) == 0 {
		// 节点集合在运行期不变，理论不可达，保留为防御性判定。
		s.logf("input Schedule group=%q replica=%q reason=%v", groupID, replicaID, ErrNoMatchingNode)
		return Replica{}, ErrNoMatchingNode
	}

	// 区计数最小值只在合格区集合上计算；无匹配节点的区不参与。
	minCount := 0
	first := true
	for z := range eligible {
		c := g.zoneCount[z]
		if first || c < minCount {
			minCount, first = c, false
		}
	}

	// 在允许的区（有空槽且偏斜校验通过）中取区计数最小者，
	// 区计数并列按区标识升序；区内取剩余槽位最多的节点，并列按节点标识升序。
	var bestNode *nodeState
	bestZone := ""
	bestCount := 0
	for z := range eligible {
		zcount := g.zoneCount[z]
		if zcount+1-minCount > g.skew {
			continue
		}
		var pick *nodeState
		for _, n := range s.nodes {
			if n.spec.Zone != z || !nodeSatisfies(n.spec.Labels, g.needs) {
				continue
			}
			if n.used >= n.spec.Slots {
				continue
			}
			remaining := n.spec.Slots - n.used
			pickRemaining := 0
			if pick != nil {
				pickRemaining = pick.spec.Slots - pick.used
			}
			if pick == nil || remaining > pickRemaining ||
				(remaining == pickRemaining && n.spec.ID < pick.spec.ID) {
				pick = n
			}
		}
		if pick == nil {
			// 已满但计数最小的合格区：不能放置，也不抬高最小值。
			s.logf("judge group=%q replica=%q zone=%q count=%d full-skip (eligible-min=%d skew=%d)",
				groupID, replicaID, z, zcount, minCount, g.skew)
			continue
		}
		if bestNode == nil || zcount < bestCount || (zcount == bestCount && z < bestZone) {
			bestZone, bestNode = z, pick
			bestCount = zcount
		}
	}
	if bestNode == nil {
		s.logf("input Schedule group=%q replica=%q eligible-zones=%d min-count=%d reason=%v",
			groupID, replicaID, len(eligible), minCount, ErrUnschedulable)
		return Replica{}, ErrUnschedulable
	}

	// 调度成功即预留：在同一临界区内原子占用槽位并增加区计数。
	bestNode.used++
	g.zoneCount[bestZone]++
	g.replicas[replicaID] = &replicaState{
		id:    replicaID,
		zone:  bestZone,
		node:  bestNode.spec.ID,
		state: stateReserved,
	}
	r := Replica{Group: groupID, ID: replicaID, Zone: bestZone, Node: bestNode.spec.ID}
	s.logf("input Schedule group=%q replica=%q eligible-zones=%d min-count=%d judge zone=%q count-after=%d node=%q remaining-after=%d output=%+v",
		groupID, replicaID, len(eligible), minCount, bestZone, g.zoneCount[bestZone],
		bestNode.spec.ID, bestNode.spec.Slots-bestNode.used, r)
	return r, nil
}

// Bind 对预留执行先预留后绑定的异步绑定；失败即释放预留。
// 返回的 channel 在绑定完成后关闭，其中最多包含一个结果错误。
func (s *Scheduler) Bind(ctx context.Context, groupID, replicaID string, bind BindFunc) <-chan error {
	out := make(chan error, 1)
	if bind == nil {
		bind = func(context.Context, string, string, string) error { return nil }
	}
	s.mu.Lock()
	g, ok := s.groups[groupID]
	if !ok {
		s.mu.Unlock()
		s.logf("input Bind group=%q replica=%q reason=%v", groupID, replicaID, ErrUnknownGroup)
		out <- ErrUnknownGroup
		close(out)
		return out
	}
	r, ok := g.replicas[replicaID]
	if !ok {
		s.mu.Unlock()
		s.logf("input Bind group=%q replica=%q reason=%v", groupID, replicaID, ErrUnknownReplica)
		out <- ErrUnknownReplica
		close(out)
		return out
	}
	switch r.state {
	case stateBound:
		s.mu.Unlock()
		s.logf("input Bind group=%q replica=%q node=%q reason=%v", groupID, replicaID, r.node, ErrReservationBound)
		out <- ErrReservationBound
		close(out)
		return out
	case stateReleased:
		s.mu.Unlock()
		s.logf("input Bind group=%q replica=%q node=%q reason=%v", groupID, replicaID, r.node, ErrReservationReleased)
		out <- ErrReservationReleased
		close(out)
		return out
	case stateBinding:
		s.mu.Unlock()
		s.logf("input Bind group=%q replica=%q node=%q reason=%v", groupID, replicaID, r.node, ErrBindingInFlight)
		out <- ErrBindingInFlight
		close(out)
		return out
	}
	r.state = stateBinding
	nodeID, zoneID := r.node, r.zone
	s.mu.Unlock()

	s.logf("input Bind group=%q replica=%q node=%q phase=start", groupID, replicaID, nodeID)
	go func() {
		err := bind(ctx, groupID, replicaID, nodeID)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			// 绑定失败：释放预留，槽位与区计数一并回退，不留任何占用。
			n := s.nodes[nodeID]
			if n != nil {
				n.used--
			}
			g.zoneCount[zoneID]--
			r.state = stateReleased
			usedAfter := 0
			if n != nil {
				usedAfter = n.used
			}
			s.logf("output Bind group=%q replica=%q node=%q bind-err=%v judge release node-used-after=%d zone-count-after=%d",
				groupID, replicaID, nodeID, err, usedAfter, g.zoneCount[zoneID])
			out <- err
			close(out)
			return
		}
		r.state = stateBound
		s.logf("output Bind group=%q replica=%q node=%q bound node-used=%d zone-count=%d",
			groupID, replicaID, nodeID, s.nodes[nodeID].used, g.zoneCount[zoneID])
		close(out)
	}()
	return out
}

// Delete 删除副本，只减计数，不触发任何重新平衡。
func (s *Scheduler) Delete(groupID, replicaID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[groupID]
	if !ok {
		s.logf("input Delete group=%q replica=%q reason=%v", groupID, replicaID, ErrUnknownGroup)
		return ErrUnknownGroup
	}
	r, ok := g.replicas[replicaID]
	if !ok {
		s.logf("input Delete group=%q replica=%q reason=%v", groupID, replicaID, ErrUnknownReplica)
		return ErrUnknownReplica
	}
	if r.state == stateBinding {
		s.logf("input Delete group=%q replica=%q reason=%v", groupID, replicaID, ErrBindingInFlight)
		return ErrBindingInFlight
	}
	// 删除副本本身：释放它占用的槽位并把区计数减一；
	// 不搬运、不重新放置任何其他副本（不触发重新平衡）。
	if n := s.nodes[r.node]; n != nil {
		n.used--
	}
	g.zoneCount[r.zone]--
	delete(g.replicas, replicaID)
	s.logf("input Delete group=%q replica=%q node=%q zone=%q state=%d judge count-after=%d output ok (no rebalance)",
		groupID, replicaID, r.node, r.zone, r.state, g.zoneCount[r.zone])
	return nil
}

// Stats 返回观测快照，仅用于测试与诊断。
type Stats struct {
	NodeUsed  map[string]int
	ZoneCount map[string]int
}

// StatsOf 返回某组的占用与区计数快照。
func (s *Scheduler) StatsOf(groupID string) (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[groupID]
	if !ok {
		return Stats{}, ErrUnknownGroup
	}
	st := Stats{NodeUsed: make(map[string]int), ZoneCount: make(map[string]int)}
	for id, n := range s.nodes {
		st.NodeUsed[id] = n.used
	}
	for z, c := range g.zoneCount {
		st.ZoneCount[z] = c
	}
	return st, nil
}

func nodeSatisfies(labels, needs map[string]string) bool {
	for k, v := range needs {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func (s *Scheduler) logf(format string, args ...any) {
	if s.log != nil {
		s.log.Printf(format, args...)
	}
}
