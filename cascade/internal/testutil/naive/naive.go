// Package naive 是级联删除规则的“独立朴素参考实现”，仅用于差分测试。
//
// 它刻意与生产实现 cascade 不共享任何代码：每次操作后对全体对象做
// 反复全量扫描，直接按自然语言规则推导，直到没有任何变化（不动点）。
// 规则动作只有单调的两类（进入删除中、被移除），故任何扫描顺序都
// 收敛到同一稳定状态；每轮扫描中先统一计算再统一应用。
package naive

import (
	"fmt"
	"sort"
)

type Strategy int

const (
	Background Strategy = iota + 1
	Foreground
	Orphan
)

type OwnerRef struct {
	OwnerID  string
	Blocking bool
}

type Object struct {
	ID         string
	Owners     []OwnerRef
	Finalizers map[string]struct{}
	Deleting   bool
	Strategy   Strategy
	DeleteAt   int64
}

type ErrorKind int

const (
	InvalidArgument ErrorKind = iota + 1
	NotFound
	Conflict
	Cycle
	OwnerMissing
)

type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%d: %s", e.Kind, e.Msg) }

type Model struct {
	Objects map[string]*Object
	Clock   int64
}

func New() *Model {
	return &Model{Objects: map[string]*Object{}}
}

func (m *Model) alive(id string) bool {
	o, ok := m.Objects[id]
	return ok && o != nil
}

func copyRefs(refs []OwnerRef) []OwnerRef {
	out := append([]OwnerRef(nil), refs...)
	sort.Slice(out, func(i, j int) bool { return out[i].OwnerID < out[j].OwnerID })
	return out
}

// validate 严格复刻错误优先级：参数非法 > 不存在 > 冲突 > 环 > 属主缺失。
func (m *Model) validate(self string, refs []OwnerRef, needExist bool) *Error {
	seen := map[string]struct{}{}
	for _, r := range refs {
		if r.OwnerID == "" {
			return &Error{InvalidArgument, "empty owner"}
		}
		if r.OwnerID == self {
			return &Error{Cycle, "self reference"}
		}
		if _, ok := seen[r.OwnerID]; ok {
			return &Error{InvalidArgument, "duplicate owner"}
		}
		seen[r.OwnerID] = struct{}{}
	}
	if needExist {
		if _, ok := m.Objects[self]; !ok {
			return &Error{NotFound, "missing object"}
		}
		if m.Objects[self].Deleting {
			return &Error{Conflict, "deleting"}
		}
	}
	if m.cycle(self, refs, map[string]int{}) {
		return &Error{Cycle, "cycle"}
	}
	for _, r := range refs {
		o, ok := m.Objects[r.OwnerID]
		if !ok {
			return &Error{OwnerMissing, "missing owner"}
		}
		if o.Deleting {
			return &Error{OwnerMissing, "deleting owner"}
		}
	}
	return nil
}

func (m *Model) cycle(self string, newRefs []OwnerRef, color map[string]int) bool {
	// 从 self 出发 DFS，能回到 self 即成环；缺失属主无出边。
	var visit func(id string) bool
	visit = func(id string) bool {
		var refs []OwnerRef
		if id == self {
			refs = newRefs
		} else if o, ok := m.Objects[id]; ok {
			refs = o.Owners
		}
		for _, r := range refs {
			if r.OwnerID == self {
				return true
			}
			switch color[r.OwnerID] {
			case 1:
				return true
			case 2:
				continue
			}
			color[r.OwnerID] = 1
			if visit(r.OwnerID) {
				return true
			}
			color[r.OwnerID] = 2
		}
		return false
	}
	color[self] = 1
	return visit(self)
}

func (m *Model) Create(id string, refs []OwnerRef, fins []string) *Error {
	if id == "" {
		return &Error{InvalidArgument, "empty id"}
	}
	for _, f := range fins {
		if f == "" {
			return &Error{InvalidArgument, "empty finalizer"}
		}
	}
	if _, ok := m.Objects[id]; ok {
		return &Error{InvalidArgument, "exists"}
	}
	if e := m.validate(id, refs, false); e != nil {
		return e
	}
	o := &Object{
		ID:         id,
		Owners:     copyRefs(refs),
		Finalizers: map[string]struct{}{},
	}
	for _, f := range fins {
		o.Finalizers[f] = struct{}{}
	}
	m.Objects[id] = o
	return nil
}

func (m *Model) Delete(id string, st Strategy) *Error {
	if id == "" {
		return &Error{InvalidArgument, "empty id"}
	}
	if st < Background || st > Orphan {
		return &Error{InvalidArgument, "bad strategy"}
	}
	if _, ok := m.Objects[id]; !ok {
		return &Error{NotFound, "missing"}
	}
	m.Clock++
	o := m.Objects[id]
	if !o.Deleting {
		o.Deleting = true
		o.Strategy = st
		o.DeleteAt = m.Clock
	} else if o.Strategy == Background && st == Foreground {
		o.Strategy = Foreground
	}
	m.settle()
	return nil
}

func (m *Model) AddFinalizer(id, name string) *Error {
	if id == "" || name == "" {
		return &Error{InvalidArgument, "empty"}
	}
	o, ok := m.Objects[id]
	if !ok {
		return &Error{NotFound, "missing"}
	}
	if o.Deleting {
		return &Error{Conflict, "deleting"}
	}
	o.Finalizers[name] = struct{}{}
	return nil
}

func (m *Model) RemoveFinalizer(id, name string) *Error {
	if id == "" || name == "" {
		return &Error{InvalidArgument, "empty"}
	}
	o, ok := m.Objects[id]
	if !ok {
		return &Error{NotFound, "missing"}
	}
	if _, ok := o.Finalizers[name]; !ok {
		return &Error{NotFound, "no finalizer"}
	}
	delete(o.Finalizers, name)
	m.Clock++
	m.settle()
	return nil
}

func (m *Model) ReplaceOwners(id string, refs []OwnerRef) *Error {
	if id == "" {
		return &Error{InvalidArgument, "empty id"}
	}
	if _, ok := m.Objects[id]; !ok {
		return &Error{NotFound, "missing"}
	}
	if e := m.validate(id, refs, true); e != nil {
		return e
	}
	m.Objects[id].Owners = copyRefs(refs)
	// 属主边变化可能解除某个删除中对象的阻塞条件；任何改变状态的
	// 操作返回前都必须收敛，故这里同样推进到稳定状态。
	m.settle()
	return nil
}

// settle 全量反复扫描，直到一轮中无任何变化。
func (m *Model) settle() {
	for {
		changed := false

		// 规则 A：前台传播闭包。在不做任何移除的前提下反复扫描，直到
		// 不再有新对象被标记，保证多跳前台链（含经“将在本轮稍后移除”
		// 的阻塞依赖者的中转）完整闭包后，才进入规则 B 的移除阶段。
		for {
			propChanged := false
			for _, o := range m.sortedObjects() {
				if o.Deleting && o.Strategy != Background {
					continue
				}
				fg, alive, deleting := 0, 0, 0
				for _, r := range o.Owners {
					if p, ok := m.Objects[r.OwnerID]; ok {
						alive++
						if p.Deleting {
							deleting++
						}
						if p.Deleting && p.Strategy == Foreground {
							fg++
						}
					}
				}
				if alive > 0 && fg > 0 && alive == deleting {
					o.Deleting = true
					o.Strategy = Foreground
					o.DeleteAt = m.Clock
					changed = true
					propChanged = true
				}
			}
			if !propChanged {
				break
			}
		}

		// 规则 B：移除可移除对象，并对依赖者摘边/连带删除。
		// 逐个移除前基于“当前”状态重新确认阻塞条件：前一个对象的移除
		// 可能摘除本对象的边（含阻塞边），使本对象在本轮变为可移除。
		var removals []string
		for _, o := range m.sortedObjects() {
			if m.removable(o) {
				removals = append(removals, o.ID)
			}
		}
		for _, id := range removals {
			o, ok := m.Objects[id]
			if !ok || !m.removable(o) {
				continue
			}
			for _, d := range m.Objects {
				j := -1
				for i, r := range d.Owners {
					if r.OwnerID == id {
						j = i
					}
				}
				if j < 0 {
					continue
				}
				d.Owners = append(d.Owners[:j], d.Owners[j+1:]...)
				if o.Strategy != Orphan && len(d.Owners) == 0 && !d.Deleting {
					d.Deleting = true
					d.Strategy = Background
					d.DeleteAt = m.Clock
				}
			}
			delete(m.Objects, id)
			changed = true
		}

		if !changed {
			return
		}
	}
}

// removable 按当前状态判断对象是否立即可移除。
func (m *Model) removable(o *Object) bool {
	if !o.Deleting || len(o.Finalizers) > 0 {
		return false
	}
	if o.Strategy != Foreground {
		return true
	}
	for _, d := range m.Objects {
		for _, r := range d.Owners {
			if r.OwnerID == o.ID && r.Blocking {
				return false
			}
		}
	}
	return true
}

func (m *Model) sortedObjects() []*Object {
	ids := make([]string, 0, len(m.Objects))
	for id := range m.Objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*Object, 0, len(ids))
	for _, id := range ids {
		out = append(out, m.Objects[id])
	}
	return out
}

// Canonical 返回可比较的规范化状态。
func (m *Model) Canonical() map[string]string {
	out := map[string]string{}
	for id, o := range m.Objects {
		fins := make([]string, 0, len(o.Finalizers))
		for f := range o.Finalizers {
			fins = append(fins, f)
		}
		sort.Strings(fins)
		refs := ""
		for _, r := range o.Owners {
			b := "0"
			if r.Blocking {
				b = "1"
			}
			refs += r.OwnerID + ":" + b + ","
		}
		del := "0"
		if o.Deleting {
			del = "1"
		}
		out[id] = fmt.Sprintf("owners=[%s]fins=%vdel=%sst=%d", refs, fins, del, o.Strategy)
	}
	return out
}
