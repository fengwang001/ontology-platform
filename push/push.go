// Package push 负责变更集应用、下发任务编号、确认与撤销。
//
// 不变量：任一时刻每台现存设备
//   - 有 Pd 当且仅当 E != A，且 Pd 内容恒等于当前 E；
//   - pushId 全局连续无洞，仅实际建号时占号；
//   - 每个任务恰有一个结局（Acked/Cancelled/Superseded）或仍 Pending。
package push

import (
	"errors"
	"sort"
	"sync"

	"ontology/group"
	"ontology/policy"
)

// ErrStale 表示 Ack/Nack 的 pushId 与当前待确认任务不匹配。
var ErrStale = errors.New("stale push id")

// Status 是任务结局。
type Status int

const (
	Pending Status = iota
	Acked
	Cancelled
	Superseded
)

func (st Status) String() string {
	switch st {
	case Acked:
		return "Acked"
	case Cancelled:
		return "Cancelled"
	case Superseded:
		return "Superseded"
	default:
		return "Pending"
	}
}

// Task 是一个下发任务的最终/当前记录。
type Task struct {
	PushID int
	Dev    group.Name
	Config map[group.Name]string
	Status Status
	Nacks  int
}

// Config 是键到字符串值的有效配置。
type Config = map[group.Name]string

type devState struct {
	ack Config // A：已确认配置
	pd  Config // Pd：当前待确认任务内容（nil 表示无待确认任务）
	pid int    // Pd 的 pushId（pd==nil 时为 0）
}

// Service 拥有 group.Store 与全部下发状态；一把锁串行化一切。
type Service struct {
	mu    sync.Mutex
	gmax  int
	store *group.Store
	devs  map[group.Name]*devState
	tasks map[int]*Task
	next  int

	// recomputed 为非导出观测计数：最终对齐阶段求值的设备台数。
	recomputed int
}

// New 创建服务，gmax 必须在 1..16。
func New(gmax int) *Service {
	if gmax < 1 || gmax > 16 {
		panic("push: gmax out of range")
	}
	return &Service{
		gmax:  gmax,
		store: group.New(),
		devs:  map[group.Name]*devState{},
		tasks: map[int]*Task{},
	}
}

// Store 返回底层 group 存储（只读快照/直接 Apply 时使用）。
func (s *Service) Store() *group.Store { return s.store }

// Apply 原子执行变更集，全部成功后对受影响设备按批后状态对齐一次。
func (s *Service) Apply(ops []group.Op) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.store.ApplyLocked(ops, s.gmax); err != nil {
		return err
	}

	s.alignBatch(ops)
	return nil
}

// alignBatch 必须在 group 变更已成功、锁仍持有时调用。
func (s *Service) alignBatch(ops []group.Op) {
	snap := s.store.SnapshotLocked()

	// 结构变化：设备增删。
	for _, op := range ops {
		switch op.Kind {
		case group.AddDevice:
			s.devs[op.Dev] = &devState{ack: Config{}}
		case group.RemoveDevice:
			if d, ok := s.devs[op.Dev]; ok {
				if d.pid != 0 {
					s.tasks[d.pid].Status = Cancelled
					d.pid = 0
					d.pd = nil
				}
				delete(s.devs, op.Dev)
			}
		}
	}

	// 受影响现存设备集合。
	affected := map[group.Name]struct{}{}
	for _, op := range ops {
		switch op.Kind {
		case group.AddDevice, group.RemoveDevice, group.AddMember, group.RemoveMember:
			affected[op.Dev] = struct{}{}
		case group.SetPolicy, group.SetPriority:
			if op.Group == group.Star {
				for d := range s.devs {
					affected[d] = struct{}{}
				}
			} else if gs, ok := snap.Groups[op.Group]; ok {
				for _, m := range gs.Members {
					affected[m] = struct{}{}
				}
			}
		}
	}

	names := make([]group.Name, 0, len(affected))
	for d := range affected {
		if _, ok := s.devs[d]; ok {
			names = append(names, d)
		}
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })

	for _, d := range names {
		s.recomputed++
		e := policy.EffectiveLocked(s.store, d)
		s.alignDev(s.devs[d], d, e)
	}
}

// alignDev 按求值结果对齐单台设备。调用方持锁。
func (s *Service) alignDev(d *devState, dev group.Name, e Config) {
	if mapEqual(e, d.ack) {
		if d.pid != 0 {
			s.tasks[d.pid].Status = Cancelled
			d.pid = 0
			d.pd = nil
		}
		return
	}
	if d.pid != 0 && mapEqual(e, d.pd) {
		return
	}
	if d.pid != 0 {
		s.tasks[d.pid].Status = Superseded
		d.pid = 0
		d.pd = nil
	}
	s.next++
	pid := s.next
	cfg := cloneConfig(e)
	d.pid = pid
	d.pd = cfg
	s.tasks[pid] = &Task{PushID: pid, Dev: dev, Config: cfg, Status: Pending}
}

func mapEqual(a, b Config) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[k]
		if !ok || bv != v {
			return false
		}
	}
	return true
}

func cloneConfig(c Config) Config {
	out := make(Config, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out
}

// Ack 确认设备当前待确认任务。
func (s *Service) Ack(dev group.Name, pushID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devs[dev]
	if !ok || d.pid == 0 || d.pid != pushID {
		return ErrStale
	}
	d.ack = cloneConfig(d.pd)
	t := s.tasks[pushID]
	t.Status = Acked
	d.pid = 0
	d.pd = nil
	return nil
}

// Nack 拒绝设备当前待确认任务：只增加失败计数，任务保留。
func (s *Service) Nack(dev group.Name, pushID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devs[dev]
	if !ok || d.pid == 0 || d.pid != pushID {
		return ErrStale
	}
	s.tasks[pushID].Nacks++
	return nil
}

// 以下为单操作便捷方法，等价于只含该操作的变更集。

// AddDevice 添加设备并立即对齐。
func (s *Service) AddDevice(dev group.Name) error {
	return s.Apply([]group.Op{{Kind: group.AddDevice, Dev: dev}})
}

// RemoveDevice 移除设备，其待确认任务记为 Cancelled。
func (s *Service) RemoveDevice(dev group.Name) error {
	return s.Apply([]group.Op{{Kind: group.RemoveDevice, Dev: dev}})
}

// AddGroup 新建分组。
func (s *Service) AddGroup(name group.Name, pr int) error {
	return s.Apply([]group.Op{{Kind: group.AddGroup, Group: name, PR: pr}})
}

// RemoveGroup 删除空分组（* 拒绝）。
func (s *Service) RemoveGroup(name group.Name) error {
	return s.Apply([]group.Op{{Kind: group.RemoveGroup, Group: name}})
}

// AddMember 将设备加入分组，重复报 ErrExists。
func (s *Service) AddMember(grp, dev group.Name) error {
	return s.Apply([]group.Op{{Kind: group.AddMember, Group: grp, Dev: dev}})
}

// RemoveMember 将设备移出分组，不存在报 ErrNotFound。
func (s *Service) RemoveMember(grp, dev group.Name) error {
	return s.Apply([]group.Op{{Kind: group.RemoveMember, Group: grp, Dev: dev}})
}

// SetPolicy 整份替换分组策略。
func (s *Service) SetPolicy(grp group.Name, p group.Policy) error {
	return s.Apply([]group.Op{{Kind: group.SetPolicy, Group: grp, Policy: p}})
}

// SetPriority 修改分组优先级（* 拒绝）。
func (s *Service) SetPriority(grp group.Name, pr int) error {
	return s.Apply([]group.Op{{Kind: group.SetPriority, Group: grp, PR: pr}})
}
