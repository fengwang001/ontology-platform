// Package group 维护设备、分组、成员关系与分组策略的纯状态。
//
// Store 自带读写锁：包外直接调用 Apply/Snapshot 会自行加锁；
// policy、push 包在已持锁临界区内调用 applyLocked/SnapshotLocked。
package group

import (
	"errors"
	"sort"
	"strconv"
	"sync"
)

// PolicySource 是求值所需的锁内只读视图：由 policy 包在临界区调用，
// 回调内不得修改状态、不得再次取锁。
type PolicySource interface {
	// IterDeviceGroups 遍历 dev 显式所属分组及内置分组 * 的
	// (组名, 优先级, 策略)；dev 不存在时回调不执行。
	IterDeviceGroups(dev Name, fn func(group Name, pr int, policy Policy))
}

// Name 是 1..64 字节的非空字节串（按字节序比较）。
type Name string

// Star 是内置分组名。
const Star Name = "*"

// Value 是策略值：Unset 为显式置空标记，否则取 Val（可为空串）。
type Value struct {
	Unset bool
	Val   string
}

// Policy 是键到值的整份映射（0..32 项）。
type Policy map[Name]Value

// OpKind 标识变更操作种类。
type OpKind int

const (
	AddDevice OpKind = iota + 1
	RemoveDevice
	AddGroup
	RemoveGroup
	AddMember
	RemoveMember
	SetPolicy
	SetPriority
)

// Op 是一个变更操作。Group 为分组名，Dev 为设备名，
// PR 为 AddGroup/SetPriority 的目标优先级，Policy 为 SetPolicy 的整份替换。
type Op struct {
	Kind   OpKind
	Group  Name
	Dev    Name
	PR     int
	Policy Policy
}

// 错误原因（用 errors.Is 判定）。
var (
	ErrInvalid       = errors.New("invalid argument")
	ErrNotFound      = errors.New("not found")
	ErrExists        = errors.New("already exists")
	ErrNotEmpty      = errors.New("group not empty")
	ErrTooManyGroups = errors.New("too many device groups")
)

// OpError 携带变更集内首个失败操作的下标与原因。
// 下标 -1 表示变更集本身非法（空或超过 256 个操作）。
type OpError struct {
	Index int
	Err   error
}

func (e *OpError) Error() string {
	if e == nil {
		return ""
	}
	return "op[" + strconv.Itoa(e.Index) + "]: " + e.Err.Error()
}

func (e *OpError) Unwrap() error { return e.Err }

// DeviceState 是快照中的设备状态：显式所属分组，按名字节序排列。
type DeviceState struct {
	Groups []Name
}

// GroupState 是快照中的分组状态。
type GroupState struct {
	Name    Name
	PR      int
	Builtin bool
	Members []Name
	Policy  Policy
}

// Snapshot 是某一时刻的完整只读状态（切片均按字节序排序）。
type Snapshot struct {
	Devices map[Name]DeviceState
	Groups  map[Name]GroupState
}

type groupData struct {
	pr      int
	builtin bool
	policy  Policy
	members map[Name]struct{}
}

// Store 是全部 group 状态的归属者。
type Store struct {
	mu      sync.RWMutex
	devices map[Name]map[Name]struct{}
	groups  map[Name]*groupData
}

// New 创建含内置分组 *（pr=-1、空策略）的空存储。
func New() *Store {
	s := &Store{
		devices: map[Name]map[Name]struct{}{},
		groups:  map[Name]*groupData{},
	}
	s.groups[Star] = &groupData{
		pr:      -1,
		builtin: true,
		policy:  Policy{},
		members: map[Name]struct{}{},
	}
	return s
}

// Lock/Unlock 暴露给 policy、push 在同一临界区内使用。
func (s *Store) Lock()   { s.mu.Lock() }
func (s *Store) Unlock() { s.mu.Unlock() }

// RLock/RUnlock 供 policy 包只读求值使用。
func (s *Store) RLock()   { s.mu.RLock() }
func (s *Store) RUnlock() { s.mu.RUnlock() }

// ApplyLocked 在调用方已持锁时执行变更集，失败整体回滚。
func (s *Store) ApplyLocked(ops []Op, gmax int) error {
	return s.applyLocked(ops, gmax)
}

// Apply 加锁执行变更集，任一失败整体回滚并返回 *OpError。
func (s *Store) Apply(ops []Op, gmax int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(ops, gmax)
}

type journalEntry struct {
	kind OpKind
	dev  Name
	grp  Name

	devMembership map[Name]struct{} // RemoveDevice 前的成员关系
	oldGroup      *groupData        // RemoveGroup 前的分组
	oldPolicy     Policy            // SetPolicy 前
	oldPR         int               // SetPriority 前
}

func (s *Store) applyLocked(ops []Op, gmax int) error {
	if len(ops) < 1 || len(ops) > 256 {
		return &OpError{Index: -1, Err: ErrInvalid}
	}
	journal := make([]journalEntry, 0, len(ops))

	for i, op := range ops {
		if err := s.validate(op, gmax); err != nil {
			for j := len(journal) - 1; j >= 0; j-- {
				s.undo(journal[j])
			}
			return &OpError{Index: i, Err: err}
		}
		journal = append(journal, s.exec(op))
	}
	return nil
}

func validName(n Name) bool {
	return len(n) >= 1 && len(n) <= 64
}

func validPolicy(p Policy) bool {
	if len(p) > 32 {
		return false
	}
	for k := range p {
		if !validName(k) {
			return false
		}
	}
	return true
}

func validPR(pr int) bool { return pr >= 0 && pr <= 1000 }

func (s *Store) validate(op Op, gmax int) error {
	switch op.Kind {
	case AddDevice, RemoveDevice:
		if !validName(op.Dev) {
			return ErrInvalid
		}
		_, exists := s.devices[op.Dev]
		if op.Kind == AddDevice && exists {
			return ErrExists
		}
		if op.Kind == RemoveDevice && !exists {
			return ErrNotFound
		}

	case AddGroup:
		if !validName(op.Group) || !validPR(op.PR) {
			return ErrInvalid
		}
		if _, ok := s.groups[op.Group]; ok {
			return ErrExists
		}

	case RemoveGroup:
		if !validName(op.Group) {
			return ErrInvalid
		}
		g, ok := s.groups[op.Group]
		if !ok {
			return ErrNotFound
		}
		if g.builtin {
			return ErrInvalid
		}
		if len(g.members) > 0 {
			return ErrNotEmpty
		}

	case AddMember, RemoveMember:
		if !validName(op.Group) || !validName(op.Dev) {
			return ErrInvalid
		}
		g, gok := s.groups[op.Group]
		if !gok {
			return ErrNotFound
		}
		if g.builtin {
			return ErrInvalid
		}
		d, dok := s.devices[op.Dev]
		if !dok {
			return ErrNotFound
		}
		_, member := d[op.Group]
		if op.Kind == AddMember {
			if member {
				return ErrExists
			}
			if len(d) >= gmax {
				return ErrTooManyGroups
			}
		} else if !member {
			return ErrNotFound
		}

	case SetPolicy:
		if !validName(op.Group) || !validPolicy(op.Policy) {
			return ErrInvalid
		}
		if _, ok := s.groups[op.Group]; !ok {
			return ErrNotFound
		}

	case SetPriority:
		if !validName(op.Group) || !validPR(op.PR) {
			return ErrInvalid
		}
		g, ok := s.groups[op.Group]
		if !ok {
			return ErrNotFound
		}
		if g.builtin {
			return ErrInvalid
		}

	default:
		return ErrInvalid
	}
	return nil
}

func clonePolicy(p Policy) Policy {
	c := make(Policy, len(p))
	for k, v := range p {
		c[k] = v
	}
	return c
}

// exec 执行已通过 validate 的操作并返回 undo 日志项。
func (s *Store) exec(op Op) journalEntry {
	switch op.Kind {
	case AddDevice:
		s.devices[op.Dev] = map[Name]struct{}{}
		return journalEntry{kind: op.Kind, dev: op.Dev}

	case RemoveDevice:
		mem := s.devices[op.Dev]
		e := journalEntry{kind: op.Kind, dev: op.Dev, devMembership: mem}
		for g := range mem {
			delete(s.groups[g].members, op.Dev)
		}
		delete(s.devices, op.Dev)
		return e

	case AddGroup:
		s.groups[op.Group] = &groupData{
			pr:      op.PR,
			policy:  Policy{},
			members: map[Name]struct{}{},
		}
		return journalEntry{kind: op.Kind, grp: op.Group}

	case RemoveGroup:
		g := s.groups[op.Group]
		delete(s.groups, op.Group)
		return journalEntry{kind: op.Kind, grp: op.Group, oldGroup: g}

	case AddMember:
		s.devices[op.Dev][op.Group] = struct{}{}
		s.groups[op.Group].members[op.Dev] = struct{}{}
		return journalEntry{kind: op.Kind, grp: op.Group, dev: op.Dev}

	case RemoveMember:
		delete(s.devices[op.Dev], op.Group)
		delete(s.groups[op.Group].members, op.Dev)
		return journalEntry{kind: op.Kind, grp: op.Group, dev: op.Dev}

	case SetPolicy:
		g := s.groups[op.Group]
		e := journalEntry{kind: op.Kind, grp: op.Group, oldPolicy: g.policy}
		g.policy = clonePolicy(op.Policy)
		return e

	case SetPriority:
		g := s.groups[op.Group]
		e := journalEntry{kind: op.Kind, grp: op.Group, oldPR: g.pr}
		g.pr = op.PR
		return e
	}
	return journalEntry{}
}

func (s *Store) undo(e journalEntry) {
	switch e.kind {
	case AddDevice:
		delete(s.devices, e.dev)

	case RemoveDevice:
		s.devices[e.dev] = e.devMembership
		for g := range e.devMembership {
			s.groups[g].members[e.dev] = struct{}{}
		}

	case AddGroup:
		delete(s.groups, e.grp)

	case RemoveGroup:
		s.groups[e.grp] = e.oldGroup

	case AddMember:
		delete(s.devices[e.dev], e.grp)
		delete(s.groups[e.grp].members, e.dev)

	case RemoveMember:
		s.devices[e.dev][e.grp] = struct{}{}
		s.groups[e.grp].members[e.dev] = struct{}{}

	case SetPolicy:
		s.groups[e.grp].policy = e.oldPolicy

	case SetPriority:
		s.groups[e.grp].pr = e.oldPR
	}
}

// Snapshot 在外部调用时加读锁返回完整深拷贝快照。
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SnapshotLocked()
}

// IterDeviceGroups 实现 PolicySource。调用方必须已持有读锁或写锁。
func (s *Store) IterDeviceGroups(dev Name, fn func(group Name, pr int, policy Policy)) {
	mem, ok := s.devices[dev]
	if !ok {
		return
	}
	star := s.groups[Star]
	fn(Star, star.pr, star.policy)
	for g := range mem {
		gd := s.groups[g]
		fn(g, gd.pr, gd.policy)
	}
}

// SnapshotLocked 供 push 临界区内调用（调用方已持写锁）。
func (s *Store) SnapshotLocked() Snapshot {
	snap := Snapshot{
		Devices: make(map[Name]DeviceState, len(s.devices)),
		Groups:  make(map[Name]GroupState, len(s.groups)),
	}
	for d, mem := range s.devices {
		groups := make([]Name, 0, len(mem))
		for g := range mem {
			groups = append(groups, g)
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
		snap.Devices[d] = DeviceState{Groups: groups}
	}
	for name, g := range s.groups {
		members := make([]Name, 0, len(g.members))
		for m := range g.members {
			members = append(members, m)
		}
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		snap.Groups[name] = GroupState{
			Name:    name,
			PR:      g.pr,
			Builtin: g.builtin,
			Members: members,
			Policy:  clonePolicy(g.policy),
		}
	}
	return snap
}
