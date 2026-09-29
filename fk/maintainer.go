// Package fk 实现变更流上的外键约束维护器。
//
// 维护器消费父表 / 子表的变更事件，保证任意时刻子表引用的父行
// 始终存在：非法操作被整体拒绝且不改变任何状态，被拒子行不被
// 记忆，需由上游重投。
package fk

import (
	"fmt"
	"sort"
	"sync"
)

// OpKind 标识变更流上的四类操作。
type OpKind string

const (
	OpInsertParent OpKind = "InsertParent"
	OpDeleteParent OpKind = "DeleteParent"
	OpInsertChild  OpKind = "InsertChild"
	OpDeleteChild  OpKind = "DeleteChild"
)

// Reason 是操作被拒绝的可区分原因。
type Reason string

const (
	// ReasonParentMissing 子行插入时父行当前不存在。
	ReasonParentMissing Reason = "PARENT_MISSING"
	// ReasonParentReferenced 删除仍被子行引用的父行。
	ReasonParentReferenced Reason = "PARENT_REFERENCED"
	// ReasonParentNotFound 删除不存在的父行。
	ReasonParentNotFound Reason = "PARENT_NOT_FOUND"
	// ReasonChildNotFound 删除不存在的子行。
	ReasonChildNotFound Reason = "CHILD_NOT_FOUND"
)

// RejectError 表示一次被整体拒绝的操作，携带可区分的原因。
type RejectError struct {
	Op     OpKind
	Reason Reason
	Key    string
	Detail string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("fk: %s 拒绝: %s (key=%s): %s", e.Op, e.Reason, e.Key, e.Detail)
}

// Operation 是变更流上的一条变更事件。
type Operation struct {
	Kind     OpKind
	ParentID string
	ChildID  string
	Fields   map[string]string
}

// ParentRow 是父表视图中的一行。
type ParentRow struct {
	ID       string
	Fields   map[string]string
	RefCount int
}

// ChildRow 是子表视图中的一行。
type ChildRow struct {
	ID       string
	ParentID string
	Fields   map[string]string
}

type childRecord struct {
	parentID string
	fields   map[string]string
}

// Maintainer 维护父表、子表与父行引用计数，可并发读写。
type Maintainer struct {
	mu       sync.RWMutex
	parents  map[string]map[string]string
	children map[string]childRecord
	refCount map[string]int
}

// New 返回一个空的维护器。
func New() *Maintainer {
	return &Maintainer{
		parents:  make(map[string]map[string]string),
		children: make(map[string]childRecord),
		refCount: make(map[string]int),
	}
}

// Apply 按操作类型分发并应用一条变更事件。
func (m *Maintainer) Apply(op Operation) error {
	switch op.Kind {
	case OpInsertParent:
		return m.InsertParent(op.ParentID, op.Fields)
	case OpDeleteParent:
		return m.DeleteParent(op.ParentID)
	case OpInsertChild:
		return m.InsertChild(op.ChildID, op.ParentID, op.Fields)
	case OpDeleteChild:
		return m.DeleteChild(op.ChildID)
	default:
		return fmt.Errorf("fk: 未知操作类型 %q", op.Kind)
	}
}

// InsertParent 幂等插入父行：已存在时不改变状态并成功返回。
func (m *Maintainer) InsertParent(id string, fields map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.parents[id]; ok {
		return nil
	}
	m.parents[id] = cloneFields(fields)
	m.refCount[id] = 0
	return nil
}

// DeleteParent 删除父行；不存在或被引用时整体拒绝。
//
// 判定顺序：先检查存在性（ReasonParentNotFound），再检查引用计数
// （ReasonParentReferenced）。任一检查失败都不改变任何状态。
func (m *Maintainer) DeleteParent(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.parents[id]; !ok {
		return &RejectError{
			Op:     OpDeleteParent,
			Reason: ReasonParentNotFound,
			Key:    id,
			Detail: "父行不存在",
		}
	}
	if n := m.refCount[id]; n > 0 {
		return &RejectError{
			Op:     OpDeleteParent,
			Reason: ReasonParentReferenced,
			Key:    id,
			Detail: fmt.Sprintf("父行仍被 %d 条子行引用", n),
		}
	}
	delete(m.parents, id)
	delete(m.refCount, id)
	return nil
}

// InsertChild 插入子行；父行当前不存在时整体拒绝且不记忆该行。
//
// 判定顺序：先检查父行当前是否存在（ReasonParentMissing），再检查
// 子行是否已存在（幂等成功）。被拒的子行不留下任何记录，需由上游
// 在父行到达后重投。
func (m *Maintainer) InsertChild(childID, parentID string, fields map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.parents[parentID]; !ok {
		return &RejectError{
			Op:     OpInsertChild,
			Reason: ReasonParentMissing,
			Key:    childID,
			Detail: fmt.Sprintf("引用的父行 %s 当前不存在", parentID),
		}
	}
	if _, ok := m.children[childID]; ok {
		return nil
	}
	m.children[childID] = childRecord{parentID: parentID, fields: cloneFields(fields)}
	m.refCount[parentID]++
	return nil
}

// DeleteChild 删除子行并递减父行引用计数；子行不存在时整体拒绝。
func (m *Maintainer) DeleteChild(childID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.children[childID]
	if !ok {
		return &RejectError{
			Op:     OpDeleteChild,
			Reason: ReasonChildNotFound,
			Key:    childID,
			Detail: "子行不存在",
		}
	}
	delete(m.children, childID)
	m.refCount[rec.parentID]--
	return nil
}

// ParentView 返回父表视图，按 ID 排序，每行均为深拷贝。
func (m *Maintainer) ParentView() []ParentRow {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := sortedKeys(m.parents)
	view := make([]ParentRow, 0, len(keys))
	for _, id := range keys {
		view = append(view, ParentRow{
			ID:       id,
			Fields:   cloneFields(m.parents[id]),
			RefCount: m.refCount[id],
		})
	}
	return view
}

// ChildView 返回子表视图，按 ID 排序，每行均为深拷贝。
func (m *Maintainer) ChildView() []ChildRow {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := sortedKeys(m.children)
	view := make([]ChildRow, 0, len(keys))
	for _, id := range keys {
		rec := m.children[id]
		view = append(view, ChildRow{
			ID:       id,
			ParentID: rec.parentID,
			Fields:   cloneFields(rec.fields),
		})
	}
	return view
}

// RefCount 返回父行当前被引用的子行数。
func (m *Maintainer) RefCount(parentID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.refCount[parentID]
}

// SelfCheck 校验不变量：引用计数恰等于实际引用数，
// 且不存在指向不存在父行的子行。
func (m *Maintainer) SelfCheck() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	actual := make(map[string]int, len(m.parents))
	for id, rec := range m.children {
		if _, ok := m.parents[rec.parentID]; !ok {
			return fmt.Errorf("fk: 自检失败: 子行 %s 指向不存在的父行 %s", id, rec.parentID)
		}
		actual[rec.parentID]++
	}
	for id, n := range m.refCount {
		if actual[id] != n {
			return fmt.Errorf("fk: 自检失败: 父行 %s 引用计数=%d, 实际被引用=%d", id, n, actual[id])
		}
	}
	return nil
}

func cloneFields(fields map[string]string) map[string]string {
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		out[k] = v
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
