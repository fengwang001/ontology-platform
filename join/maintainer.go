package join

import (
	"fmt"
	"sort"
	"sync"
)

// Maintainer 是全外连接结果的增量维护器。
//
// 视图（View）、日志（Log）与自检（Verify）均可被并发读取；
// 写操作（Apply）与读操作通过读写锁互斥，并发只读同一实例得到的
// 视图逐字段相同。
type Maintainer struct {
	mu sync.RWMutex

	// 输入状态：键 -> 该侧行标识集合；行标识在某侧全局唯一。
	left  map[string]map[string]struct{}
	right map[string]map[string]struct{}
	// 行标识 -> 键 的反向索引，用于重复插入与不存在删除的判定。
	leftIndex  map[string]string
	rightIndex map[string]string

	// 增量维护的物化视图与已输出的有序变更日志。
	view map[Row]struct{}
	log  []Entry
}

// New 创建一个空的维护器。
func New() *Maintainer {
	return &Maintainer{
		left:       make(map[string]map[string]struct{}),
		right:      make(map[string]map[string]struct{}),
		leftIndex:  make(map[string]string),
		rightIndex: make(map[string]string),
		view:       make(map[Row]struct{}),
	}
}

// Apply 原子地应用一批变更。
//
// 先对整批做校验：键为空、重复插入同一行标识、删除不存在的行标识
// 分别返回互不相同的 *RejectError；任一条被拒则整批不生效，状态与
// 已输出日志均不改变。校验通过后按变更顺序逐条应用并追加变更日志，
// 返回本批产生的日志条目。
func (m *Maintainer) Apply(batch []Change) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validate(batch); err != nil {
		return nil, err
	}

	base := len(m.log)
	for _, c := range batch {
		m.applyOne(c)
	}
	return append([]Entry(nil), m.log[base:]...), nil
}

// validate 在不改变状态的前提下对整批变更做顺序模拟校验。
func (m *Maintainer) validate(batch []Change) error {
	// 批内未提交的插入/删除叠加层：有效存在性 = (已提交 && 未删) || 批内新增。
	pendingAdd := map[Side]map[string]struct{}{Left: {}, Right: {}}
	pendingDel := map[Side]map[string]struct{}{Left: {}, Right: {}}

	committed := func(s Side, row string) bool {
		if s == Left {
			_, ok := m.leftIndex[row]
			return ok
		}
		_, ok := m.rightIndex[row]
		return ok
	}
	exists := func(s Side, row string) bool {
		if _, ok := pendingAdd[s][row]; ok {
			return true
		}
		if _, ok := pendingDel[s][row]; ok {
			return false
		}
		return committed(s, row)
	}

	for i, c := range batch {
		if c.Key == "" {
			return &RejectError{Code: RejectEmptyKey, Index: i, Change: c}
		}
		switch c.Op {
		case Insert:
			if exists(c.Side, c.Row) {
				return &RejectError{Code: RejectDuplicateRow, Index: i, Change: c}
			}
			pendingAdd[c.Side][c.Row] = struct{}{}
		case Delete:
			if !exists(c.Side, c.Row) {
				return &RejectError{Code: RejectRowNotFound, Index: i, Change: c}
			}
			delete(pendingAdd[c.Side], c.Row)
			pendingDel[c.Side][c.Row] = struct{}{}
		}
	}
	return nil
}

// applyOne 应用单条已校验变更，维护输入状态、视图并追加日志。
func (m *Maintainer) applyOne(c Change) {
	if c.Side == Left {
		if c.Op == Insert {
			m.insert(Left, c.Key, c.Row)
		} else {
			m.remove(Left, c.Key, c.Row)
		}
		return
	}
	if c.Op == Insert {
		m.insert(Right, c.Key, c.Row)
	} else {
		m.remove(Right, c.Key, c.Row)
	}
}

// insert 向 side 侧的键 key 插入行 id 并增量维护视图。
//
// 对侧有行时：若本侧计数从 0 变 1（穿越零），先撤回对侧全部补位行，
// 再输出新行与对侧每行的配对行；否则只追加新行产生的配对行。
// 对侧无行时：只追加一条本侧真实、对侧空位的补位行。
func (m *Maintainer) insert(side Side, key, id string) {
	own, other := m.sets(side)
	otherIDs := sortedIDs(other[key])
	crossing := len(own[key]) == 0 && len(otherIDs) > 0

	if crossing {
		// 撤回旧形态：对侧每行的补位行（本侧为空位）。
		for _, o := range otherIDs {
			m.emit(Delete, placeholder(side, key, o))
		}
	}
	if len(otherIDs) > 0 {
		// 输出新形态：新行与对侧每行的真实配对行。
		for _, o := range otherIDs {
			m.emit(Insert, pair(side, key, id, o))
		}
	} else {
		// 对侧无行：输出本侧真实、对侧空位的补位行。
		m.emit(Insert, placeholder(otherSide(side), key, id))
	}

	if own[key] == nil {
		own[key] = make(map[string]struct{})
	}
	own[key][id] = struct{}{}
	m.index(side)[id] = key
}

// remove 从 side 侧的键 key 删除行 id 并增量维护视图。
//
// 对侧有行时：先撤回该行与对侧每行的配对行；若本侧计数从 1 变 0
// （穿越零），再输出对侧每行的补位行（本侧为空位）。
// 对侧无行时：只撤回本侧真实、对侧空位的补位行。
func (m *Maintainer) remove(side Side, key, id string) {
	own, other := m.sets(side)
	otherIDs := sortedIDs(other[key])
	crossing := len(own[key]) == 1 && len(otherIDs) > 0

	if len(otherIDs) > 0 {
		for _, o := range otherIDs {
			m.emit(Delete, pair(side, key, id, o))
		}
		if crossing {
			// 切换新形态：对侧每行的补位行（本侧为空位）。
			for _, o := range otherIDs {
				m.emit(Insert, placeholder(side, key, o))
			}
		}
	} else {
		m.emit(Delete, placeholder(otherSide(side), key, id))
	}

	delete(own[key], id)
	if len(own[key]) == 0 {
		delete(own, key)
	}
	delete(m.index(side), id)
}

// emit 追加一条日志并同步维护视图；撤回的行必须是当前视图中存在的行。
func (m *Maintainer) emit(op Op, row Row) {
	if op == Insert {
		m.view[row] = struct{}{}
	} else {
		if _, ok := m.view[row]; !ok {
			panic(fmt.Sprintf("join: internal error, retracting absent row %+v", row))
		}
		delete(m.view, row)
	}
	m.log = append(m.log, Entry{Seq: len(m.log), Op: op, Row: row})
}

// pair 构造真实配对行：side 侧为 id，另一侧为 o。
func pair(side Side, key, id, o string) Row {
	if side == Left {
		return Row{Key: key, Left: id, HasLeft: true, Right: o, HasRight: true}
	}
	return Row{Key: key, Left: o, HasLeft: true, Right: id, HasRight: true}
}

// placeholder 构造补位行：nullSide 一侧为空位，另一侧为真实行 id。
// 左行单独存在时空位在右侧（placeholder(Right, ...)），
// 右行单独存在时空位在左侧（placeholder(Left, ...)）。
func placeholder(nullSide Side, key, id string) Row {
	if nullSide == Right {
		return Row{Key: key, Left: id, HasLeft: true}
	}
	return Row{Key: key, Right: id, HasRight: true}
}

func otherSide(s Side) Side {
	if s == Left {
		return Right
	}
	return Left
}

func (m *Maintainer) sets(side Side) (own, other map[string]map[string]struct{}) {
	if side == Left {
		return m.left, m.right
	}
	return m.right, m.left
}

func (m *Maintainer) index(side Side) map[string]string {
	if side == Left {
		return m.leftIndex
	}
	return m.rightIndex
}

func sortedIDs(set map[string]struct{}) []string {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// lessRow 定义视图快照的确定性排序。
func lessRow(a, b Row) bool {
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	if a.HasLeft != b.HasLeft {
		return !a.HasLeft
	}
	if a.Left != b.Left {
		return a.Left < b.Left
	}
	if a.HasRight != b.HasRight {
		return !a.HasRight
	}
	return a.Right < b.Right
}

// View 返回当前物化视图的快照，按确定性顺序排序。
// 返回的是副本，调用方可安全持有与遍历。
func (m *Maintainer) View() []Row {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rows := make([]Row, 0, len(m.view))
	for r := range m.view {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return lessRow(rows[i], rows[j]) })
	return rows
}

// Log 返回已输出变更日志的副本，按 Seq 升序。
func (m *Maintainer) Log() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Entry(nil), m.log...)
}

// Verify 自检：由输入状态全量重算视图，与增量维护的视图逐字段比对；
// 同时从头回放变更日志，确认下游按序应用得到的物化视图与之一致。
func (m *Maintainer) Verify() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	recomputed := make(map[Row]struct{})
	keys := make(map[string]struct{}, len(m.left)+len(m.right))
	for k := range m.left {
		keys[k] = struct{}{}
	}
	for k := range m.right {
		keys[k] = struct{}{}
	}
	for k := range keys {
		ls, rs := m.left[k], m.right[k]
		switch {
		case len(ls) > 0 && len(rs) > 0:
			for l := range ls {
				for r := range rs {
					recomputed[Row{Key: k, Left: l, HasLeft: true, Right: r, HasRight: true}] = struct{}{}
				}
			}
		case len(ls) > 0:
			for l := range ls {
				recomputed[Row{Key: k, Left: l, HasLeft: true}] = struct{}{}
			}
		default:
			for r := range rs {
				recomputed[Row{Key: k, Right: r, HasRight: true}] = struct{}{}
			}
		}
	}
	if !equalRowSet(recomputed, m.view) {
		return fmt.Errorf("join: verify failed, maintained view diverges from batch recompute (maintained=%d recomputed=%d)", len(m.view), len(recomputed))
	}

	replayed := make(map[Row]struct{})
	for _, e := range m.log {
		if e.Op == Insert {
			replayed[e.Row] = struct{}{}
		} else {
			if _, ok := replayed[e.Row]; !ok {
				return fmt.Errorf("join: verify failed, log entry %d retracts absent row %+v", e.Seq, e.Row)
			}
			delete(replayed, e.Row)
		}
	}
	if !equalRowSet(replayed, m.view) {
		return fmt.Errorf("join: verify failed, log replay diverges from maintained view")
	}
	return nil
}

func equalRowSet(a, b map[Row]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for r := range a {
		if _, ok := b[r]; !ok {
			return false
		}
	}
	return true
}
