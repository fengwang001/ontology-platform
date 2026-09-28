// Package filterview 维护一个行级过滤视图：源表中取值落在左闭右开区间
// [low, high) 内的行构成视图。支持插入、删除、更新的增量维护，并输出
// 可按顺序重放的净变化日志。
package filterview

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Row 是源表中的一行。ID 为主键，Value 为参与区间过滤的取值。
type Row struct {
	ID    string
	Value int64
}

// OpType 标识批处理条目的操作类型。
type OpType int

const (
	OpInsert OpType = iota + 1
	OpDelete
	OpUpdate
)

// Entry 是一条输入操作。
//
//   - Insert: After 为待插入行，Before 必须为零值。
//   - Delete: Before.ID 指定待删除行；After 必须为零值。
//   - Update: Before 为前像，After 为后像，两者 ID 必须相同。
type Entry struct {
	Op     OpType
	Before Row
	After  Row
}

// ChangeKind 标识输出变化的种类。
type ChangeKind int

const (
	ChangeRetract ChangeKind = iota + 1 // 从视图撤回
	ChangeAdd                           // 写入视图
)

// Change 是一条净变化输出。同一更新先输出 Retract 再输出 Add。
type Change struct {
	Kind ChangeKind
	Row  Row
}

// View 是并发安全的增量过滤视图维护器。
type View struct {
	mu     sync.RWMutex
	low    int64
	high   int64
	source map[string]Row
	view   map[string]Row
	log    []Change
}

var (
	// ErrInvalidRange 表示过滤区间参数非法（low > high）。
	ErrInvalidRange = errors.New("filterview: invalid range: low > high")
	// ErrInvalidEntry 表示输入条目本身非法（空操作、字段缺失等）。
	ErrInvalidEntry = errors.New("filterview: invalid entry")
	// ErrIDMismatch 表示更新前后主键不同。
	ErrIDMismatch = errors.New("filterview: update before/after id mismatch")
	// ErrDuplicateKey 表示插入主键已存在，或同一批内对同一键存在冲突操作。
	ErrDuplicateKey = errors.New("filterview: duplicate key")
	// ErrNotFound 表示删除/更新目标主键在源表中不存在。
	ErrNotFound = errors.New("filterview: key not found")
	// ErrPreimageMismatch 表示更新前像与源表当前行不一致。
	ErrPreimageMismatch = errors.New("filterview: update preimage does not match current row")
)

// New 创建区间为 [low, high) 的过滤视图。
func New(low, high int64) (*View, error) {
	if low > high {
		return nil, ErrInvalidRange
	}
	return &View{
		low:    low,
		high:   high,
		source: make(map[string]Row),
		view:   make(map[string]Row),
	}, nil
}

// Contains 报告取值 value 是否落入过滤区间。
func (v *View) Contains(value int64) bool {
	return value >= v.low && value < v.high
}

// ApplyBatch 原子地应用一个输入批，返回该批产生的净变化（有序）。
// 批被拒绝时源表、视图与日志均不发生变化。
func (v *View) ApplyBatch(entries []Entry) ([]Change, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	working := make(map[string]Row, len(v.source)+len(entries))
	for id, row := range v.source {
		working[id] = row
	}

	changes := make([]Change, 0, len(entries)*2)
	for i, entry := range entries {
		cs, err := v.applyEntry(working, entry)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		changes = append(changes, cs...)
	}

	v.source = working
	v.view = make(map[string]Row, len(working))
	for id, row := range working {
		if v.Contains(row.Value) {
			v.view[id] = row
		}
	}
	v.log = append(v.log, changes...)

	out := make([]Change, len(changes))
	copy(out, changes)
	return out, nil
}

func (v *View) applyEntry(working map[string]Row, entry Entry) ([]Change, error) {
	switch entry.Op {
	case OpInsert:
		if entry.After.ID == "" {
			return nil, fmt.Errorf("%w: insert with empty id", ErrInvalidEntry)
		}
		if entry.Before != (Row{}) {
			return nil, fmt.Errorf("%w: insert must not carry a before image", ErrInvalidEntry)
		}
		if _, exists := working[entry.After.ID]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateKey, entry.After.ID)
		}
		working[entry.After.ID] = entry.After
		if v.Contains(entry.After.Value) {
			return []Change{{Kind: ChangeAdd, Row: entry.After}}, nil
		}
		return nil, nil

	case OpDelete:
		if entry.Before.ID == "" {
			return nil, fmt.Errorf("%w: delete with empty id", ErrInvalidEntry)
		}
		if entry.After != (Row{}) {
			return nil, fmt.Errorf("%w: delete must not carry an after image", ErrInvalidEntry)
		}
		current, exists := working[entry.Before.ID]
		if !exists {
			return nil, fmt.Errorf("%w: %q", ErrNotFound, entry.Before.ID)
		}
		delete(working, entry.Before.ID)
		if v.Contains(current.Value) {
			return []Change{{Kind: ChangeRetract, Row: current}}, nil
		}
		return nil, nil

	case OpUpdate:
		if entry.Before.ID == "" || entry.After.ID == "" {
			return nil, fmt.Errorf("%w: update with empty id", ErrInvalidEntry)
		}
		if entry.Before.ID != entry.After.ID {
			return nil, fmt.Errorf("%w: %q vs %q", ErrIDMismatch, entry.Before.ID, entry.After.ID)
		}
		current, exists := working[entry.Before.ID]
		if !exists {
			return nil, fmt.Errorf("%w: %q", ErrNotFound, entry.Before.ID)
		}
		if current != entry.Before {
			return nil, fmt.Errorf("%w: %q", ErrPreimageMismatch, entry.Before.ID)
		}

		oldIn := v.Contains(entry.Before.Value)
		newIn := v.Contains(entry.After.Value)
		working[entry.After.ID] = entry.After

		switch {
		case oldIn && newIn:
			if entry.Before.Value == entry.After.Value {
				return nil, nil // 两侧都在视图内且值不变：无净变化
			}
			return []Change{
				{Kind: ChangeRetract, Row: entry.Before},
				{Kind: ChangeAdd, Row: entry.After},
			}, nil
		case oldIn && !newIn:
			return []Change{{Kind: ChangeRetract, Row: entry.Before}}, nil
		case !oldIn && newIn:
			return []Change{{Kind: ChangeAdd, Row: entry.After}}, nil
		default:
			return nil, nil
		}

	default:
		return nil, fmt.Errorf("%w: unknown op %d", ErrInvalidEntry, entry.Op)
	}
}

// SourceSnapshot 返回源表当前内容的快照副本，按主键升序。
func (v *View) SourceSnapshot() []Row {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return sortedRows(v.source)
}

// ViewSnapshot 返回过滤视图当前内容的快照副本，按主键升序。
func (v *View) ViewSnapshot() []Row {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return sortedRows(v.view)
}

// Log 返回截至目前全部净变化日志的副本，按产生顺序排列。
func (v *View) Log() []Change {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]Change, len(v.log))
	copy(out, v.log)
	return out
}

func sortedRows(m map[string]Row) []Row {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Row, 0, len(ids))
	for _, id := range ids {
		out = append(out, m[id])
	}
	return out
}
