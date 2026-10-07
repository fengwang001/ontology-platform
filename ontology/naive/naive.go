// Package naive 是用于对照测试的朴素参考模型。
//
// 它独立维护全部实例的当前实际版本与全部历史对应关系(从不折叠),
// 每次读取都从头重放全部历史变更重新计算视图。它的实现直观但昂贵,
// 被视作规范的正确性基准:优化实现(router+migration+backfill)在任意
// 随机操作序列下的行为必须与本模型逐条一致。
package naive

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
)

var (
	// ErrInvalid 对应参数非法。
	ErrInvalid = errors.New("invalid argument")
	// ErrNotFound 对应实例不存在。
	ErrNotFound = errors.New("instance not found")
)

// Version 是读写请求依据的结构版本。
type Version int

const (
	Old Version = iota
	New
)

// Action 是属性对应方式。
type Action int

const (
	Retain Action = iota
	AddDefault
	Deprecate
)

// Mapping 是单条属性对应关系。
type Mapping struct {
	Property string
	Action   Action
	Default  any
}

// composed 是从全部历史变更重放出的合成视图,每次用到都重新计算。
type composed struct {
	deprecated map[string]bool
	retained   map[string]bool
	added      map[string]any
}

type instance struct {
	data     map[string]any
	migrated bool
}

// Model 是朴素参考模型。
type Model struct {
	history   [][]Mapping // 全部历史变更,从不折叠
	effective map[string]bool
	instances map[string]*instance
	lastOps   int // 最近一次重放历史变更的代价(重放的对应关系条数)
}

// NewModel 创建一个空的朴素模型。
func NewModel() *Model {
	return &Model{
		effective: make(map[string]bool),
		instances: make(map[string]*instance),
	}
}

// LastOps 返回最近一次视图计算重放历史变更的代价,
// 该值随历史变更次数线性增长——这正是优化实现要避免的。
func (m *Model) LastOps() int { return m.lastOps }

// compose 从头重放全部历史对应关系。假定历史在追加时已通过校验。
func (m *Model) compose() *composed {
	c := &composed{
		deprecated: make(map[string]bool),
		retained:   make(map[string]bool),
		added:      make(map[string]any),
	}
	m.lastOps = 0
	for _, batch := range m.history {
		for _, mp := range batch {
			m.lastOps++
			delete(c.deprecated, mp.Property)
			delete(c.retained, mp.Property)
			delete(c.added, mp.Property)
			switch mp.Action {
			case Retain:
				c.retained[mp.Property] = true
			case Deprecate:
				c.deprecated[mp.Property] = true
			case AddDefault:
				c.added[mp.Property] = mp.Default
			}
		}
	}
	return c
}

func sameMapping(a, b Mapping) bool {
	return a.Action == b.Action && reflect.DeepEqual(a.Default, b.Default)
}

// Amend 校验并追加一批对应关系到历史末尾。
func (m *Model) Amend(batch []Mapping) error {
	if len(batch) == 0 {
		return fmt.Errorf("%w: empty batch", ErrInvalid)
	}
	deduped := make([]Mapping, 0, len(batch))
	seen := map[string]Mapping{}
	for _, mp := range batch {
		if mp.Property == "" {
			return fmt.Errorf("%w: empty property", ErrInvalid)
		}
		if mp.Action == AddDefault && mp.Default == nil {
			return fmt.Errorf("%w: %q needs a default", ErrInvalid, mp.Property)
		}
		if mp.Action != AddDefault && mp.Default != nil {
			return fmt.Errorf("%w: %q must not carry a default", ErrInvalid, mp.Property)
		}
		if prev, ok := seen[mp.Property]; ok {
			if !sameMapping(prev, mp) {
				return fmt.Errorf("%w: contradictory mappings for %q", ErrInvalid, mp.Property)
			}
			continue
		}
		seen[mp.Property] = mp
		deduped = append(deduped, mp)
	}
	c := m.compose()
	conflicts := func(mp Mapping) bool {
		switch mp.Action {
		case Retain:
			return c.deprecated[mp.Property] || has(c.added, mp.Property)
		case Deprecate:
			return c.retained[mp.Property] || has(c.added, mp.Property)
		case AddDefault:
			if c.deprecated[mp.Property] || c.retained[mp.Property] {
				return true
			}
			def, ok := c.added[mp.Property]
			return ok && !reflect.DeepEqual(def, mp.Default)
		}
		return false
	}
	for _, mp := range deduped {
		if conflicts(mp) && m.effective[mp.Property] {
			return fmt.Errorf("%w: mapping for %q already took effect", ErrInvalid, mp.Property)
		}
	}
	m.history = append(m.history, deduped)
	return nil
}

func has[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

func (m *Model) markEffective(c *composed, oldData map[string]any) {
	for p := range c.added {
		m.effective[p] = true
	}
	for p := range oldData {
		if c.deprecated[p] || c.retained[p] {
			m.effective[p] = true
		}
	}
}

func forward(c *composed, oldData map[string]any) map[string]any {
	out := make(map[string]any, len(oldData)+len(c.added))
	for p, v := range oldData {
		if c.deprecated[p] {
			continue
		}
		out[p] = v
	}
	for p, def := range c.added {
		out[p] = def
	}
	return out
}

func reverse(c *composed, newData map[string]any) map[string]any {
	out := make(map[string]any, len(newData))
	for p, v := range newData {
		if has(c.added, p) {
			continue
		}
		out[p] = v
	}
	return out
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for p, v := range in {
		out[p] = v
	}
	return out
}

// Create 创建实例,语义与 router.Service.Create 一致。
func (m *Model) Create(id string, v Version, props map[string]any) error {
	c := m.compose()
	for p := range props {
		if v == New && c.deprecated[p] {
			return fmt.Errorf("%w: %q deprecated", ErrInvalid, p)
		}
		if v == Old && has(c.added, p) {
			return fmt.Errorf("%w: %q new-only", ErrInvalid, p)
		}
	}
	if _, ok := m.instances[id]; ok {
		return fmt.Errorf("%w: %q exists", ErrInvalid, id)
	}
	m.instances[id] = &instance{data: copyMap(props), migrated: v == New}
	return nil
}

// Read 每次调用都重放全部历史对应关系重新计算视图。
func (m *Model) Read(id string, v Version) (map[string]any, error) {
	c := m.compose()
	inst, ok := m.instances[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	switch {
	case inst.migrated && v == Old:
		return reverse(c, inst.data), nil
	case !inst.migrated && v == New:
		return forward(c, inst.data), nil
	default:
		return copyMap(inst.data), nil
	}
}

// Write 写入,任何写入都把实例转换为新版本结构。
func (m *Model) Write(id string, v Version, props map[string]any) error {
	c := m.compose()
	for p := range props {
		if c.deprecated[p] {
			return fmt.Errorf("%w: %q deprecated", ErrInvalid, p)
		}
		if v == Old && has(c.added, p) {
			return fmt.Errorf("%w: %q new-only", ErrInvalid, p)
		}
	}
	inst, ok := m.instances[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if !inst.migrated {
		oldData := inst.data
		base := oldData
		if v == New {
			base = forward(c, oldData)
		}
		next := copyMap(base)
		for p, val := range props {
			next[p] = val
		}
		if v == Old {
			next = forward(c, next)
		}
		inst.data = next
		inst.migrated = true
		m.markEffective(c, oldData)
	} else {
		base := inst.data
		if v == Old {
			base = reverse(c, base)
		}
		next := copyMap(base)
		for p, val := range props {
			next[p] = val
		}
		if v == Old {
			next = forward(c, next)
		}
		inst.data = next
	}
	return nil
}

// Delete 删除实例。
func (m *Model) Delete(id string) error {
	if _, ok := m.instances[id]; !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	delete(m.instances, id)
	return nil
}

// Status 返回实例是否存在及是否已回填。
func (m *Model) Status(id string) (exists, migrated bool) {
	inst, ok := m.instances[id]
	if !ok {
		return false, false
	}
	return true, inst.migrated
}

// IDs 按字典序返回全部现存实例 ID。
func (m *Model) IDs() []string {
	ids := make([]string, 0, len(m.instances))
	for id := range m.instances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// BackfillNext 按字典序回填下一个未回填实例,返回实例 ID 与是否已无待回填。
func (m *Model) BackfillNext() (string, bool) {
	c := m.compose()
	var ids []string
	for id, inst := range m.instances {
		if !inst.migrated {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return "", true
	}
	sort.Strings(ids)
	id := ids[0]
	inst := m.instances[id]
	m.markEffective(c, inst.data)
	inst.data = forward(c, inst.data)
	inst.migrated = true
	return id, false
}
