// Package filterview 增量维护一个行级过滤视图。
//
// 视图由源表中满足左闭右开取值区间 [Low, High) 的行组成。对源行的
// 插入、删除、更新以批（Batch）为单位提交：整批要么全部生效，要么在
// 任一输入非法时整体拒绝，且拒绝不会改动源表、视图与已产生的日志。
package filterview

import (
	"sync"
	"sync/atomic"
)

// Row 是源表中的一行，Key 为主键，Value 为参与区间判定的取值。
type Row struct {
	Key   string
	Value int64
}

// Interval 是左闭右开的取值区间 [Low, High)。
type Interval struct {
	Low  int64
	High int64
}

// OpKind 标识批中一条输入操作的类型。
type OpKind int

const (
	// OpInsert 插入一行（After 为新行）。
	OpInsert OpKind = iota + 1
	// OpDelete 删除一行（Before 为前像，必须与源表当前行相等）。
	OpDelete
	// OpUpdate 更新一行（Before 为前像，After 为后像）。
	OpUpdate
)

// Op 是批中的一条操作。
//
//   - Insert：只填 After。
//   - Delete：只填 Before，且 Before 必须与源表当前行逐字段相等。
//   - Update：填 Before 与 After，二者主键必须相同。
type Op struct {
	Kind   OpKind
	Before Row
	After  Row
}

// ChangeKind 标识对过滤视图的一条净变化。
type ChangeKind int

const (
	// ViewInsert 表示一行进入视图（写入新值）。
	ViewInsert ChangeKind = iota + 1
	// ViewDelete 表示一行离开视图（撤回旧值）。
	ViewDelete
)

// Change 是视图的一条净变化。更新在需要时产生两条变化：
// 先 ViewDelete 撤回旧值，再 ViewInsert 写入新值。
type Change struct {
	Kind ChangeKind
	Row  Row
}

// Maintainer 维护源表与过滤视图，所有方法可被并发调用。
type Maintainer struct {
	mu       sync.RWMutex
	interval Interval
	source   map[string]Row
	view     map[string]Row
	seq      uint64
	logger   atomic.Pointer[Logger]
}

// New 创建一个以 [low, high) 为过滤条件的维护器。
func New(low, high int64) (*Maintainer, error) {
	if low >= high {
		return nil, ErrInvalidInterval
	}
	m := &Maintainer{
		interval: Interval{Low: low, High: high},
		source:   make(map[string]Row),
		view:     make(map[string]Row),
	}
	return m, nil
}

type pendingKind int

const (
	pendingSet pendingKind = iota + 1
	pendingDelete
)

type pending struct {
	kind pendingKind
	row  Row
}

// Apply 原子地应用一批操作。整批合法时，返回视图的净变化；
// 整批非法时，返回可区分原因的错误且不产生任何副作用。
func (m *Maintainer) Apply(ops []Op) ([]Change, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	changes := make([]Change, 0)
	pendingRows := make(map[string]pending)
	logs := make([]string, 0, len(ops))

	current := func(key string) (Row, bool) {
		if p, ok := pendingRows[key]; ok {
			if p.kind == pendingDelete {
				return Row{}, false
			}
			return p.row, true
		}
		r, ok := m.source[key]
		return r, ok
	}

	reject := func(i int, op Op, err error) ([]Change, error) {
		return nil, &RejectError{Index: i, Op: op, Err: err}
	}

	for i, op := range ops {
		switch op.Kind {
		case OpInsert:
			if op.After.Key == "" {
				return reject(i, op, ErrEmptyKey)
			}
			if _, exists := current(op.After.Key); exists {
				return reject(i, op, ErrDuplicateKey)
			}
			afterIn := m.containsLocked(op.After.Value)
			if afterIn {
				changes = append(changes, Change{Kind: ViewInsert, Row: op.After})
			}
			pendingRows[op.After.Key] = pending{kind: pendingSet, row: op.After}
			logs = append(logs, m.decideInsert(i, op.After, afterIn))

		case OpDelete:
			if op.Before.Key == "" {
				return reject(i, op, ErrEmptyKey)
			}
			cur, exists := current(op.Before.Key)
			if !exists {
				return reject(i, op, ErrKeyNotFound)
			}
			if cur != op.Before {
				return reject(i, op, ErrBeforeMismatch)
			}
			beforeIn := m.containsLocked(cur.Value)
			if beforeIn {
				changes = append(changes, Change{Kind: ViewDelete, Row: cur})
			}
			pendingRows[cur.Key] = pending{kind: pendingDelete}
			logs = append(logs, m.decideDelete(i, cur, beforeIn))

		case OpUpdate:
			if op.Before.Key == "" || op.After.Key == "" {
				return reject(i, op, ErrEmptyKey)
			}
			if op.Before.Key != op.After.Key {
				return reject(i, op, ErrUpdateKeyMismatch)
			}
			cur, exists := current(op.Before.Key)
			if !exists {
				return reject(i, op, ErrKeyNotFound)
			}
			if cur != op.Before {
				return reject(i, op, ErrBeforeMismatch)
			}
			beforeIn := m.containsLocked(cur.Value)
			afterIn := m.containsLocked(op.After.Value)
			sameRow := cur == op.After
			basis := updateBasis(beforeIn, afterIn, sameRow)
			switch basis {
			case updateInToOut:
				changes = append(changes, Change{Kind: ViewDelete, Row: cur})
			case updateOutToIn:
				changes = append(changes, Change{Kind: ViewInsert, Row: op.After})
			case updateInToInChanged:
				changes = append(changes,
					Change{Kind: ViewDelete, Row: cur},
					Change{Kind: ViewInsert, Row: op.After},
				)
			}
			pendingRows[cur.Key] = pending{kind: pendingSet, row: op.After}
			logs = append(logs, m.decideUpdate(i, cur, op.After, beforeIn, afterIn, basis))

		default:
			return reject(i, op, ErrUnknownOp)
		}
	}

	// 全部校验通过后才提交：源表与视图、日志在同一临界区一次性生效。
	for key, p := range pendingRows {
		if p.kind == pendingDelete {
			delete(m.source, key)
			continue
		}
		m.source[key] = p.row
	}
	for _, c := range changes {
		switch c.Kind {
		case ViewInsert:
			m.view[c.Row.Key] = c.Row
		case ViewDelete:
			delete(m.view, c.Row.Key)
		}
	}

	m.seq++
	m.emitLogs(m.seq, logs, changes)
	return changes, nil
}

// Snapshot 返回源表与过滤视图在同一时刻的一致只读副本。
func (m *Maintainer) Snapshot() (source map[string]Row, view map[string]Row) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	source = make(map[string]Row, len(m.source))
	view = make(map[string]Row, len(m.view))
	for k, v := range m.source {
		source[k] = v
	}
	for k, v := range m.view {
		view[k] = v
	}
	return source, view
}

// Contains 报告取值 v 是否落在过滤区间内。
func (m *Maintainer) Contains(v int64) bool {
	return v >= m.interval.Low && v < m.interval.High
}

func (m *Maintainer) containsLocked(v int64) bool {
	return v >= m.interval.Low && v < m.interval.High
}

// Interval 返回维护器使用的过滤区间。
func (m *Maintainer) Interval() Interval {
	return m.interval
}
