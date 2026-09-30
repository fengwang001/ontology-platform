package fojoin

import (
	"fmt"
	"sort"
	"sync"
)

// keyState 保存一个键上两侧各自存在的行标识集合。
type keyState struct {
	left  map[string]struct{}
	right map[string]struct{}
}

// rowKey 是 Row 的可比较规范化形式，用作视图集合的键。
type rowKey struct {
	key      string
	left     string
	right    string
	hasLeft  bool
	hasRight bool
}

func toRowKey(r Row) rowKey {
	k := rowKey{key: r.Key}
	if r.LeftID != nil {
		k.left, k.hasLeft = *r.LeftID, true
	}
	if r.RightID != nil {
		k.right, k.hasRight = *r.RightID, true
	}
	return k
}

func (k rowKey) toRow() Row {
	r := Row{Key: k.key}
	if k.hasLeft {
		l := k.left
		r.LeftID = &l
	}
	if k.hasRight {
		rv := k.right
		r.RightID = &rv
	}
	return r
}

// Maintainer 增量维护全外连接结果。
//
// 内部保存两侧行集合（事实源）与由自身输出日志物化出的视图；
// 每次 Apply 先在副本上整批校验（任一条被拒则整批不生效），
// 再逐条应用变更并追加输出日志。视图与自检均可并发读取。
type Maintainer struct {
	mu   sync.RWMutex
	keys map[string]*keyState
	view map[rowKey]struct{}
	log  []LogEntry
}

// New 返回一个空的维护器。
func New() *Maintainer {
	return &Maintainer{
		keys: make(map[string]*keyState),
		view: make(map[rowKey]struct{}),
	}
}

// Apply 校验并应用一批变更，返回本批产生的输出日志条目。
// 任一条变更非法时整批不生效，状态与已输出日志保持不变。
func (m *Maintainer) Apply(batch []Change) ([]LogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validate(batch); err != nil {
		return nil, err
	}
	entries := make([]LogEntry, 0, len(batch))
	for _, c := range batch {
		entries = append(entries, m.applyOne(c)...)
	}
	m.log = append(m.log, entries...)
	return entries, nil
}

// validate 在状态副本上按序模拟整批变更，返回第一条非法变更的错误。
func (m *Maintainer) validate(batch []Change) error {
	sim := make(map[string]*keyState, len(m.keys))
	for k, ks := range m.keys {
		cp := &keyState{
			left:  make(map[string]struct{}, len(ks.left)),
			right: make(map[string]struct{}, len(ks.right)),
		}
		for id := range ks.left {
			cp.left[id] = struct{}{}
		}
		for id := range ks.right {
			cp.right[id] = struct{}{}
		}
		sim[k] = cp
	}
	for i, c := range batch {
		if c.Side != Left && c.Side != Right {
			return &RejectError{Kind: ErrUnknownSide, Index: i, Change: c}
		}
		if c.Op != Insert && c.Op != Delete {
			return &RejectError{Kind: ErrUnknownOp, Index: i, Change: c}
		}
		if c.Key == "" {
			return &RejectError{Kind: ErrEmptyKey, Index: i, Change: c}
		}
		ks := sim[c.Key]
		rows := sideRows(ks, c.Side)
		switch c.Op {
		case Insert:
			if _, ok := rows[c.RowID]; ok {
				return &RejectError{Kind: ErrDuplicateInsert, Index: i, Change: c}
			}
			rows[c.RowID] = struct{}{}
		case Delete:
			if _, ok := rows[c.RowID]; !ok {
				return &RejectError{Kind: ErrMissingDelete, Index: i, Change: c}
			}
			delete(rows, c.RowID)
		}
	}
	return nil
}

// sideRows 返回键状态上对应侧的行集合；ks 为 nil 时返回空集合。
func sideRows(ks *keyState, s Side) map[string]struct{} {
	if ks == nil {
		return map[string]struct{}{}
	}
	if s == Left {
		return ks.left
	}
	return ks.right
}

// applyOne 应用一条（已通过校验的）变更，返回产生的输出日志条目。
//
// 仅当本侧计数穿越零（0→非零 或 非零→0）时才在补足行与配对行
// 之间切换，切换时先撤回旧形态再输出新形态；其余情况只增删
// 受影响的行，不做整键重建。
func (m *Maintainer) applyOne(c Change) []LogEntry {
	ks := m.keys[c.Key]
	if ks == nil {
		ks = &keyState{left: map[string]struct{}{}, right: map[string]struct{}{}}
		m.keys[c.Key] = ks
	}
	l, r := len(ks.left), len(ks.right)

	var entries []LogEntry
	emit := func(add bool, row Row) {
		k := toRowKey(row)
		if add {
			m.view[k] = struct{}{}
		} else {
			delete(m.view, k)
		}
		entries = append(entries, LogEntry{Add: add, Row: row})
	}

	other := sortedIDs(sideRows(ks, otherSide(c.Side)))
	id := c.RowID
	if c.Op == Insert {
		switch {
		case len(other) > 0 && sideCount(l, r, c.Side) == 0:
			// 本侧 0→非零：先撤回全部补足行，再输出真实配对行。
			for _, o := range other {
				emit(false, padRow(c.Key, c.Side, o))
			}
			for _, o := range other {
				emit(true, pairRow(c.Key, c.Side, id, o))
			}
		case len(other) > 0:
			// 两侧均非零：只新增本行与对侧的配对行。
			for _, o := range other {
				emit(true, pairRow(c.Key, c.Side, id, o))
			}
		default:
			// 对侧为空：只新增一条补足行。
			emit(true, padRow(c.Key, otherSide(c.Side), id))
		}
		sideRows(ks, c.Side)[id] = struct{}{}
	} else {
		remaining := sideCount(l, r, c.Side) - 1
		switch {
		case len(other) > 0 && remaining == 0:
			// 本侧 非零→0：先撤回全部配对行，再输出补足行。
			for _, o := range other {
				emit(false, pairRow(c.Key, c.Side, id, o))
			}
			for _, o := range other {
				emit(true, padRow(c.Key, c.Side, o))
			}
		case len(other) > 0:
			// 两侧均非零：只撤回本行的配对行。
			for _, o := range other {
				emit(false, pairRow(c.Key, c.Side, id, o))
			}
		default:
			// 对侧为空：只撤回本行的补足行。
			emit(false, padRow(c.Key, otherSide(c.Side), id))
		}
		delete(sideRows(ks, c.Side), id)
	}

	if len(ks.left) == 0 && len(ks.right) == 0 {
		delete(m.keys, c.Key)
	}
	return entries
}

// sideCount 按侧返回计数。
func sideCount(l, r int, s Side) int {
	if s == Left {
		return l
	}
	return r
}

func otherSide(s Side) Side {
	if s == Left {
		return Right
	}
	return Left
}

// pairRow 构造配对行，side 表示 id 所属的一侧。
func pairRow(key string, side Side, id, other string) Row {
	if side == Left {
		return Pair(key, id, other)
	}
	return Pair(key, other, id)
}

// padRow 构造补足行：id 属于 side 的对侧，空位在 side 一侧。
// 即 padRow(key, Left, o) 表示左行撤回后、右侧 o 的补足行（空位在左）。
func padRow(key string, padSide Side, id string) Row {
	if padSide == Left {
		return RightOnly(key, id)
	}
	return LeftOnly(key, id)
}

func sortedIDs(set map[string]struct{}) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// View 返回当前物化视图的有序快照。
func (m *Maintainer) View() []Row {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return sortedRows(m.view)
}

func sortedRows(view map[rowKey]struct{}) []Row {
	rows := make([]Row, 0, len(view))
	for k := range view {
		rows = append(rows, k.toRow())
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if ls, rs := deref(a.LeftID), deref(b.LeftID); ls != rs {
			return ls < rs
		}
		return deref(a.RightID) < deref(b.RightID)
	})
	return rows
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Log 返回迄今为止全部输出日志的有序快照。
func (m *Maintainer) Log() []LogEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]LogEntry, len(m.log))
	copy(out, m.log)
	return out
}

// Recompute 从两侧行集合批量重算全外连接结果（与增量无关的参照实现）。
func Recompute(batches ...[]Change) []Row {
	m := New()
	for _, b := range batches {
		if _, err := m.Apply(b); err != nil {
			panic(err)
		}
	}
	rows := make([]Row, 0)
	for key, ks := range m.keys {
		switch {
		case len(ks.left) > 0 && len(ks.right) > 0:
			for _, l := range sortedIDs(ks.left) {
				for _, r := range sortedIDs(ks.right) {
					rows = append(rows, Pair(key, l, r))
				}
			}
		case len(ks.left) > 0:
			for _, l := range sortedIDs(ks.left) {
				rows = append(rows, LeftOnly(key, l))
			}
		default:
			for _, r := range sortedIDs(ks.right) {
				rows = append(rows, RightOnly(key, r))
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		if ls, rs := deref(a.LeftID), deref(b.LeftID); ls != rs {
			return ls < rs
		}
		return deref(a.RightID) < deref(b.RightID)
	})
	return rows
}

// SelfCheck 用批量重算校验当前增量维护的视图是否一致。
func (m *Maintainer) SelfCheck() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	expected := make(map[rowKey]struct{})
	for key, ks := range m.keys {
		switch {
		case len(ks.left) > 0 && len(ks.right) > 0:
			for l := range ks.left {
				for r := range ks.right {
					expected[toRowKey(Pair(key, l, r))] = struct{}{}
				}
			}
		case len(ks.left) > 0:
			for l := range ks.left {
				expected[toRowKey(LeftOnly(key, l))] = struct{}{}
			}
		default:
			for r := range ks.right {
				expected[toRowKey(RightOnly(key, r))] = struct{}{}
			}
		}
	}
	if len(expected) != len(m.view) {
		return fmt.Errorf("fojoin: self-check failed: view has %d rows, recompute has %d", len(m.view), len(expected))
	}
	for k := range expected {
		if _, ok := m.view[k]; !ok {
			return fmt.Errorf("fojoin: self-check failed: view missing recomputed row %+v", k.toRow())
		}
	}
	return nil
}
