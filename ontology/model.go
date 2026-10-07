package ontology

import (
	"fmt"
	"maps"
	"slices"
	"sync"
)

// Model 是独立实现的朴素参照模型：用一把全局锁串行执行所有批次，
// 逻辑与 Store 相同但不允许任何并发。随机化测试把 Store 的并发结果
// 按判定逻辑时刻重排后与 Model 的串行执行结果逐一比对。
type Model struct {
	mu        sync.Mutex
	instances map[InstanceID]*modelInstance
	limits    map[LinkTypeID]int
}

type modelInstance struct {
	version Version
	attrs   map[string]string
	links   map[LinkTypeID]map[InstanceID]struct{}
}

// NewModel 创建参照模型。
func NewModel() *Model {
	return &Model{
		instances: make(map[InstanceID]*modelInstance),
		limits:    make(map[LinkTypeID]int),
	}
}

// SetLinkLimit 设置基数约束，语义与 Store.SetLinkLimit 相同。
func (m *Model) SetLinkLimit(t LinkTypeID, max int) {
	m.limits[t] = max
}

// Create 以初始版本 1 创建实例。
func (m *Model) Create(id InstanceID) {
	if _, ok := m.instances[id]; !ok {
		m.instances[id] = &modelInstance{
			version: 1,
			attrs:   make(map[string]string),
			links:   make(map[LinkTypeID]map[InstanceID]struct{}),
		}
	}
}

// ApplyBatch 串行执行一个批次并返回判定结果（不含日志元数据）。
func (m *Model) ApplyBatch(b Batch) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()

	dec := Decision{BatchID: b.ID, Items: b.Items, Observed: make(map[InstanceID]Version)}

	seen := make(map[InstanceID]bool, len(b.Items))
	for _, it := range b.Items {
		if seen[it.Instance] {
			dec.Outcome = OutcomeDuplicatePrecondition
			dec.Detail = fmt.Sprintf("实例 %q 在批次内被重复声明", it.Instance)
			return dec
		}
		seen[it.Instance] = true
	}

	dec.Reads = len(b.Items)
	for _, it := range b.Items {
		var v Version
		if inst, ok := m.instances[it.Instance]; ok {
			v = inst.version
		}
		dec.Observed[it.Instance] = v
	}
	for _, it := range b.Items {
		v := dec.Observed[it.Instance]
		if v != it.Expect {
			dec.Outcome = OutcomeVersionConflict
			dec.Detail = fmt.Sprintf("实例 %q 期望版本 %d，实际版本 %d", it.Instance, it.Expect, v)
			return dec
		}
	}

	for _, it := range b.Items {
		inst := m.instances[it.Instance]
		touched := map[LinkTypeID]bool{}
		for _, l := range it.AddLinks {
			touched[l.Type] = true
		}
		for _, l := range it.RemoveLinks {
			touched[l.Type] = true
		}
		for lt := range touched {
			limit, bounded := m.limits[lt]
			if !bounded {
				continue
			}
			after := map[InstanceID]bool{}
			if inst != nil {
				for t := range inst.links[lt] {
					after[t] = true
				}
			}
			for _, l := range it.RemoveLinks {
				if l.Type == lt {
					delete(after, l.Target)
				}
			}
			for _, l := range it.AddLinks {
				if l.Type == lt {
					after[l.Target] = true
				}
			}
			if len(after) > limit {
				dec.Outcome = OutcomeCardinalityViolation
				dec.Detail = fmt.Sprintf("实例 %q 的关联 %q 基数 %d 超过上限 %d",
					it.Instance, lt, len(after), limit)
				return dec
			}
		}
	}

	for _, it := range b.Items {
		inst, ok := m.instances[it.Instance]
		if !ok {
			inst = &modelInstance{
				attrs: make(map[string]string),
				links: make(map[LinkTypeID]map[InstanceID]struct{}),
			}
			m.instances[it.Instance] = inst
		}
		maps.Copy(inst.attrs, it.SetAttrs)
		for _, l := range it.RemoveLinks {
			delete(inst.links[l.Type], l.Target)
		}
		for _, l := range it.AddLinks {
			set, ok := inst.links[l.Type]
			if !ok {
				set = make(map[InstanceID]struct{})
				inst.links[l.Type] = set
			}
			set[l.Target] = struct{}{}
		}
		inst.version++
	}
	dec.Outcome = OutcomeCommitted
	dec.Detail = fmt.Sprintf("已提交，涉及 %d 个实例", len(b.Items))
	return dec
}

// SnapshotOf 返回实例当前状态快照。
func (m *Model) SnapshotOf(id InstanceID) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[id]
	if !ok {
		return Snapshot{}, false
	}
	snap := Snapshot{
		Version: inst.version,
		Attrs:   maps.Clone(inst.attrs),
		Links:   make(map[LinkTypeID][]InstanceID, len(inst.links)),
	}
	for lt, set := range inst.links {
		targets := make([]InstanceID, 0, len(set))
		for t := range set {
			targets = append(targets, t)
		}
		slices.Sort(targets)
		snap.Links[lt] = targets
	}
	return snap, true
}
