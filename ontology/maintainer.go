package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// ConstraintError 描述一次被整体拒绝的操作及其可区分原因。
type ConstraintError struct {
	Kind ErrKind
	Op   Op
}

func (e *ConstraintError) Error() string {
	return fmt.Sprintf("%s: op=%s parent=%q child=%q", e.Kind, e.Op.Kind, e.Op.ParentID, e.Op.ChildID)
}

// Maintainer 在变更流上维护父表、子表与外键引用计数。
// 任一方法都可被并发调用；所有失败均为原子失败，不改变任何状态。
type Maintainer struct {
	mu sync.RWMutex
	// parents 记录当前存在的父行主键。
	parents map[string]struct{}
	// children 以子行主键为键记录子行。
	children map[string]ChildRow
	// refCount 记录每个父行当前被子行引用的次数。
	refCount map[string]int
}

// NewMaintainer 创建空的维护器。
func NewMaintainer() *Maintainer {
	return &Maintainer{
		parents:  make(map[string]struct{}),
		children: make(map[string]ChildRow),
		refCount: make(map[string]int),
	}
}

// Apply 原子地应用一条操作，返回操作后的完整视图。
// 失败时返回 *ConstraintError，且父表、子表、引用计数与返回视图均保持操作前不变。
//
// 四类操作的判定顺序：
//  1. insert_parent：父行已存在则幂等返回成功；否则插入。
//  2. delete_parent：先判定父行是否存在（不存在 -> parent_not_found），
//     再判定引用计数是否为 0（非 0 -> parent_referenced）。
//  3. insert_child：先判定父行是否存在（不存在 -> child_parent_missing，
//     被拒子行不写入任何状态，需由上游重投）；父行存在时子行幂等。
//  4. delete_child：先判定子行是否存在（不存在 -> child_not_found），
//     存在则删除并将对应父行的引用计数减 1。
func (m *Maintainer) Apply(op Op) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch op.Kind {
	case OpInsertParent:
		m.parents[op.ParentID] = struct{}{}

	case OpDeleteParent:
		if _, ok := m.parents[op.ParentID]; !ok {
			return m.snapshotLocked(), &ConstraintError{Kind: ErrParentNotFound, Op: op}
		}
		if m.refCount[op.ParentID] > 0 {
			return m.snapshotLocked(), &ConstraintError{Kind: ErrParentReferenced, Op: op}
		}
		delete(m.parents, op.ParentID)

	case OpInsertChild:
		if _, ok := m.parents[op.ParentID]; !ok {
			// 原子拒绝：不在 children/refCount 中留下任何痕迹。
			return m.snapshotLocked(), &ConstraintError{Kind: ErrChildParentMissing, Op: op}
		}
		if _, ok := m.children[op.ChildID]; !ok {
			m.children[op.ChildID] = ChildRow{ID: op.ChildID, ParentID: op.ParentID}
			m.refCount[op.ParentID]++
		}

	case OpDeleteChild:
		row, ok := m.children[op.ChildID]
		if !ok {
			return m.snapshotLocked(), &ConstraintError{Kind: ErrChildNotFound, Op: op}
		}
		delete(m.children, op.ChildID)
		m.refCount[row.ParentID]--

	default:
		return m.snapshotLocked(), &ConstraintError{Kind: ErrKind("unknown_op"), Op: op}
	}

	return m.snapshotLocked(), nil
}

// Snapshot 返回当前父表与子表按主键排序的深拷贝快照，
// 多次并发调用得到的视图逐字段一致。
func (m *Maintainer) Snapshot() View {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshotLocked()
}

func (m *Maintainer) snapshotLocked() View {
	parents := make([]ParentRow, 0, len(m.parents))
	for id := range m.parents {
		parents = append(parents, ParentRow{ID: id})
	}
	sort.Slice(parents, func(i, j int) bool { return parents[i].ID < parents[j].ID })

	children := make([]ChildRow, 0, len(m.children))
	for _, row := range m.children {
		children = append(children, row)
	}
	sort.Slice(children, func(i, j int) bool { return children[i].ID < children[j].ID })

	return View{Parents: parents, Children: children}
}

// RefCount 返回某父行当前被多少个子行引用；父行不存在时返回 0, false。
func (m *Maintainer) RefCount(parentID string) (int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.parents[parentID]; !ok {
		return 0, false
	}
	return m.refCount[parentID], true
}

// InvariantError 描述一次自检失败。
type InvariantError struct {
	Reason string
}

func (e *InvariantError) Error() string {
	return "invariant violated: " + e.Reason
}

// CheckInvariants 执行自检：
// 每个子行都指向存在的父行，且引用计数恰等于实际引用它的子行数。
func (m *Maintainer) CheckInvariants() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	actual := make(map[string]int, len(m.parents))
	for _, row := range m.children {
		if _, ok := m.parents[row.ParentID]; !ok {
			return &InvariantError{Reason: fmt.Sprintf("child %q references missing parent %q", row.ID, row.ParentID)}
		}
		actual[row.ParentID]++
	}
	for parentID, count := range m.refCount {
		if count != actual[parentID] {
			return &InvariantError{Reason: fmt.Sprintf("parent %q refcount=%d but actual children=%d", parentID, count, actual[parentID])}
		}
	}
	for parentID, count := range actual {
		if count != m.refCount[parentID] {
			return &InvariantError{Reason: fmt.Sprintf("parent %q actual children=%d but refcount=%d", parentID, count, m.refCount[parentID])}
		}
	}
	return nil
}

// ViewsEqual 逐字段比较两个视图（含顺序），供重放核对使用。
func ViewsEqual(a, b View) bool {
	if len(a.Parents) != len(b.Parents) || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Parents {
		if a.Parents[i] != b.Parents[i] {
			return false
		}
	}
	for i := range a.Children {
		if a.Children[i] != b.Children[i] {
			return false
		}
	}
	return true
}

// Replay 在一个全新的维护器上按顺序重放只包含成功操作的日志，
// 并与期望视图逐字段比较，用于本地可复现核对。
// 若日志中的任何操作被拒绝，或重放结果与 want 不一致，则 OK 为 false。
func Replay(ops []Op, want View) ReplayResult {
	m := NewMaintainer()
	var view View
	for _, op := range ops {
		v, err := m.Apply(op)
		if err != nil {
			return ReplayResult{OK: false, View: v, ErrorKind: err.(*ConstraintError).Kind}
		}
		view = v
	}
	if !ViewsEqual(view, want) {
		return ReplayResult{OK: false, View: view, ErrorKind: ErrReplayMismatch}
	}
	return ReplayResult{OK: true, View: view}
}
