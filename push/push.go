// Package push 在分组与策略之上编排下发任务、确认与原子变更集。
package push

import (
	"errors"
	"sort"
	"sync"

	"ontology/group"
	"ontology/policy"
)

// 透传 group/policy 的拒绝原因，调用方只需 import push。
var (
	ErrInvalid       = group.ErrInvalid
	ErrNotFound      = group.ErrNotFound
	ErrExists        = group.ErrExists
	ErrNotEmpty      = group.ErrNotEmpty
	ErrTooManyGroups = group.ErrTooManyGroups

	// ErrStale 表示 Ack/Nack 的 pushId 与设备当前待确认任务不符。
	ErrStale = errors.New("push: stale pushId")
)

// Kind 标识变更操作种类。
type Kind int

const (
	AddDeviceK Kind = iota + 1
	RemoveDeviceK
	AddGroupK
	RemoveGroupK
	AddMemberK
	RemoveMemberK
	SetPolicyK
	SetPriorityK
)

// Op 是一个变更操作。Policy 仅 SetPolicy 使用。
type Op struct {
	Kind     Kind
	Device   string
	Group    string
	Priority int
	Policy   map[string]policy.Value
}

// OpError 指出变更集中首个失败操作的下标与原因。
type OpError struct {
	Index int
	Err   error
}

func (e *OpError) Error() string { return e.Err.Error() }
func (e *OpError) Unwrap() error { return e.Err }

// 便利构造器。
func AddDevice(dev string) Op         { return Op{Kind: AddDeviceK, Device: dev} }
func RemoveDevice(dev string) Op      { return Op{Kind: RemoveDeviceK, Device: dev} }
func AddGroup(name string, pr int) Op { return Op{Kind: AddGroupK, Group: name, Priority: pr} }
func RemoveGroup(name string) Op      { return Op{Kind: RemoveGroupK, Group: name} }
func AddMember(g, dev string) Op      { return Op{Kind: AddMemberK, Group: g, Device: dev} }
func RemoveMember(g, dev string) Op   { return Op{Kind: RemoveMemberK, Group: g, Device: dev} }
func SetPolicy(g string, p map[string]policy.Value) Op {
	return Op{Kind: SetPolicyK, Group: g, Policy: p}
}
func SetPriority(g string, pr int) Op { return Op{Kind: SetPriorityK, Group: g, Priority: pr} }

// Outcome 是任务的最终结局。
type Outcome int

const (
	Pending Outcome = iota
	Acked
	Cancelled
	Superseded
)

// Task 记录一次下发任务。
type Task struct {
	PushID  int
	Device  string
	Config  map[string]string
	Outcome Outcome
}

type pending struct {
	pushID int
	config map[string]string
}

type devState struct {
	ack      map[string]string
	pd       *pending
	failures int
}

// Service 是下发服务。
type Service struct {
	mu     sync.Mutex
	gmax   int
	groups *group.State
	pol    *policy.Store
	devs   map[string]*devState
	nextID int
	tasks  map[int]*Task
	// recomputed 为非导出计数器：上次成功变更重算的设备数。
	recomputed int
}

// New 创建服务；gmax 为每设备显式分组数上限（1..16）。
func New(gmax int) (*Service, error) {
	st, err := group.New(gmax)
	if err != nil {
		return nil, err
	}
	return &Service{
		gmax:   gmax,
		groups: st,
		pol:    policy.New(),
		devs:   map[string]*devState{},
		nextID: 1,
		tasks:  map[int]*Task{},
	}, nil
}

// Gmax 返回设备分组数上限。
func (s *Service) Gmax() int { return s.gmax }

// Recomputed 返回上次成功变更重算的设备数。
func (s *Service) Recomputed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recomputed
}

// PendingPushID 返回设备当前待确认任务编号；无待确认任务时 ok=false。
func (s *Service) PendingPushID(dev string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ds, ok := s.devs[dev]
	if !ok || ds.pd == nil {
		return 0, false
	}
	return ds.pd.pushID, true
}

// applyOne 在候选状态上执行一个操作，返回该操作影响的设备集合。
func applyOne(gs *group.State, ps *policy.Store, op Op) (map[string]struct{}, error) {
	affected := map[string]struct{}{}
	switch op.Kind {
	case AddDeviceK:
		if err := gs.AddDevice(op.Device); err != nil {
			return nil, err
		}
		affected[op.Device] = struct{}{}
	case RemoveDeviceK:
		if err := gs.RemoveDevice(op.Device); err != nil {
			return nil, err
		}
		affected[op.Device] = struct{}{}
	case AddGroupK:
		if err := gs.AddGroup(op.Group, op.Priority); err != nil {
			return nil, err
		}
		ps.AddGroup(op.Group)
	case RemoveGroupK:
		if err := gs.RemoveGroup(op.Group); err != nil {
			return nil, err
		}
		ps.RemoveGroup(op.Group)
	case AddMemberK:
		if err := gs.AddMember(op.Group, op.Device); err != nil {
			return nil, err
		}
		affected[op.Device] = struct{}{}
	case RemoveMemberK:
		if err := gs.RemoveMember(op.Group, op.Device); err != nil {
			return nil, err
		}
		affected[op.Device] = struct{}{}
	case SetPolicyK:
		if err := ps.Set(op.Group, op.Policy); err != nil {
			return nil, err
		}
		for _, dev := range gs.MembersOf(op.Group) {
			affected[dev] = struct{}{}
		}
	case SetPriorityK:
		if err := gs.SetPriority(op.Group, op.Priority); err != nil {
			return nil, err
		}
		for _, dev := range gs.MembersOf(op.Group) {
			affected[dev] = struct{}{}
		}
	default:
		return nil, ErrInvalid
	}
	return affected, nil
}

// Apply 原子地执行 1..256 个操作：全部成功才生效，否则返回最小下标错误。
func (s *Service) Apply(ops []Op) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ops) < 1 || len(ops) > 256 {
		return &OpError{Index: 0, Err: ErrInvalid}
	}
	candG := s.groups.Clone()
	candP := s.pol.Clone()
	for i, op := range ops {
		// 前序操作在候选状态上依次生效；任一步失败即丢弃整份候选，
		// 已提交状态与 pushId 计数完全不变。
		if _, err := applyOne(candG, candP, op); err != nil {
			return &OpError{Index: i, Err: err}
		}
	}

	// 提交。
	s.groups = candG
	s.pol = candP

	// 批内删除（或先加后删）的设备：Pd 记 Cancelled，状态移除。
	for _, op := range ops {
		if op.Kind == RemoveDeviceK {
			if ds := s.devs[op.Device]; ds != nil && ds.pd != nil {
				s.tasks[ds.pd.pushID].Outcome = Cancelled
			}
			delete(s.devs, op.Device)
		}
	}

	// 批内新增（且批后仍在）的设备：初始化确认状态。
	for _, op := range ops {
		if op.Kind == AddDeviceK && s.groups.HasDevice(op.Device) {
			if _, ok := s.devs[op.Device]; !ok {
				s.devs[op.Device] = &devState{ack: map[string]string{}}
			}
		}
	}

	// 受影响集合必须按批后状态计算：例如 SetPolicy(*) 先于 AddDevice 时，
	// 新设备也是 * 的成员，应进入对齐集合；先加后删的设备因已不存在而被排除。
	affected := map[string]struct{}{}
	for _, op := range ops {
		switch op.Kind {
		case AddDeviceK, RemoveDeviceK, AddMemberK, RemoveMemberK:
			if s.groups.HasDevice(op.Device) {
				affected[op.Device] = struct{}{}
			}
		case SetPolicyK, SetPriorityK:
			for _, dev := range s.groups.MembersOf(op.Group) {
				affected[dev] = struct{}{}
			}
		}
	}

	// 仅按批后状态对受影响设备对齐一次。
	list := make([]string, 0, len(affected))
	for dev := range affected {
		list = append(list, dev)
	}
	sort.Strings(list)
	for _, dev := range list {
		s.align(dev)
	}
	s.recomputed = len(list)

	return nil
}

func sameConfig(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// align 按当前 E 与 A 对齐单台设备，调用方须持锁且设备存在。
func (s *Service) align(dev string) {
	ds := s.devs[dev]
	e := policy.Effective(s.groups, s.pol, dev)
	if sameConfig(e, ds.ack) {
		if ds.pd != nil {
			s.tasks[ds.pd.pushID].Outcome = Cancelled
			ds.pd = nil
		}
		return
	}
	if ds.pd != nil && sameConfig(e, ds.pd.config) {
		return
	}
	if ds.pd != nil {
		s.tasks[ds.pd.pushID].Outcome = Superseded
	}
	id := s.nextID
	s.nextID++
	cfg := make(map[string]string, len(e))
	for k, v := range e {
		cfg[k] = v
	}
	ds.pd = &pending{pushID: id, config: cfg}
	s.tasks[id] = &Task{PushID: id, Device: dev, Config: cfg, Outcome: Pending}
}

// Ack 确认设备当前待确认任务。
func (s *Service) Ack(dev string, pushID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ds, ok := s.devs[dev]
	if !ok || ds.pd == nil || ds.pd.pushID != pushID {
		return ErrStale
	}
	ack := make(map[string]string, len(ds.pd.config))
	for k, v := range ds.pd.config {
		ack[k] = v
	}
	ds.ack = ack
	s.tasks[pushID].Outcome = Acked
	ds.pd = nil
	return nil
}

// Nack 否认设备当前待确认任务：仅累加失败计数，任务保留。
func (s *Service) Nack(dev string, pushID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ds, ok := s.devs[dev]
	if !ok || ds.pd == nil || ds.pd.pushID != pushID {
		return ErrStale
	}
	ds.failures++
	return nil
}
