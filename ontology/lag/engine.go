package lag

import (
	"fmt"
	"sort"
	"sync"
)

// DefaultMaxRows 是单引擎允许容纳的最大行数，可用 WithMaxRows 覆盖。
const DefaultMaxRows = 1_000_000

type entry struct {
	row     Row
	hasPrev bool
	prev    string
}

// Engine 增量维护各分区内每行的前驱取值（LAG）。
//
// 所有方法均可被多个执行体并发调用；视图读取（View / Snapshot /
// TotalRows / Verify）可与提交（Insert / Delete）并发，由读写锁
// 保证每次调用都看到一致状态。
type Engine struct {
	mu      sync.RWMutex
	parts   map[string]map[string]*entry
	total   int
	maxRows int
	logf    func(format string, args ...any)
}

// Option 配置引擎。
type Option func(*Engine)

// WithMaxRows 设置引擎允许的最大行数。插入导致超限时该次插入被整体拒绝。
func WithMaxRows(n int) Option { return func(e *Engine) { e.maxRows = n } }

// WithLogger 安装逐步日志钩子，每步打印输入、输出与判定依据。
func WithLogger(f func(format string, args ...any)) Option {
	return func(e *Engine) { e.logf = f }
}

// New 创建引擎。
func New(opts ...Option) *Engine {
	e := &Engine{parts: map[string]map[string]*entry{}, maxRows: DefaultMaxRows}
	for _, opt := range opts {
		opt(e)
	}
	if e.logf == nil {
		e.logf = func(string, ...any) {}
	}
	return e
}

func (e *Engine) log(format string, args ...any) { e.logf(format, args...) }

func reject(op, part, id string, reason Reason, detail string) *RejectError {
	return &RejectError{Reason: reason, Op: op, Part: part, ID: id, Detail: detail}
}

// lessKey 定义分区内全序：排序键升序，并列时标识升序。
func lessKey(sk1 int64, id1 string, sk2 int64, id2 string) bool {
	if sk1 != sk2 {
		return sk1 < sk2
	}
	return id1 < id2
}

// orderedEntries 返回分区内按 (SortKey, ID) 升序排列的条目副本。
func orderedEntries(rows map[string]*entry) []*entry {
	out := make([]*entry, 0, len(rows))
	for _, en := range rows {
		out = append(out, en)
	}
	sort.Slice(out, func(i, j int) bool {
		return lessKey(out[i].row.SortKey, out[i].row.ID, out[j].row.SortKey, out[j].row.ID)
	})
	return out
}

// validateInsert 校验插入输入，返回互不相同的拒绝原因。
func (e *Engine) validateInsert(row Row) *RejectError {
	if row.Partition == "" {
		return reject("insert", row.Partition, row.ID, ReasonEmptyPartition,
			"partition name must not be empty")
	}
	if row.ID == "" {
		return reject("insert", row.Partition, row.ID, ReasonEmptyID,
			"row id must not be empty")
	}
	if e.total >= e.maxRows {
		return reject("insert", row.Partition, row.ID, ReasonRowLimitExceeded,
			fmt.Sprintf("total rows after insert %d would exceed limit %d", e.total+1, e.maxRows))
	}
	if rows := e.parts[row.Partition]; rows != nil {
		if _, ok := rows[row.ID]; ok {
			return reject("insert", row.Partition, row.ID, ReasonDuplicateID,
				"id already exists in partition")
		}
	}
	return nil
}

// Insert 插入一行，返回本次按固定顺序排列的变更日志：
//  1. 先输出新行自身（insert，携带其前驱值，首行为空前驱）；
//  2. 若紧邻后继的前驱取值因此改变（含 空<->非空 的变化），再输出该后继的 update；
//     前驱取值未变则后继不输出任何条目。
//
// 任何校验失败都整体拒绝：不写入行、不改变视图、不产生日志。
func (e *Engine) Insert(row Row) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.log("INPUT  insert partition=%q id=%q sortKey=%d value=%q",
		row.Partition, row.ID, row.SortKey, row.Value)

	if err := e.validateInsert(row); err != nil {
		e.log("DECIDE reject reason=%s detail=%q (state unchanged)", err.Reason, err.Detail)
		return nil, err
	}

	rows := e.parts[row.Partition]
	if rows == nil {
		rows = map[string]*entry{}
		e.parts[row.Partition] = rows
	}
	en := &entry{row: row}
	rows[row.ID] = en
	e.total++

	ordered := orderedEntries(rows)
	idx := sort.Search(len(ordered), func(i int) bool {
		return !lessKey(ordered[i].row.SortKey, ordered[i].row.ID, row.SortKey, row.ID)
	})
	var pred, succ *entry
	if idx > 0 {
		pred = ordered[idx-1]
	}
	if idx+1 < len(ordered) {
		succ = ordered[idx+1]
	}
	if pred != nil {
		en.hasPrev, en.prev = true, pred.row.Value
	}

	changes := make([]Change, 0, 2)
	changes = append(changes, Change{Kind: ChangeInsert, Partition: row.Partition, ID: row.ID,
		SortKey: row.SortKey, Value: row.Value, HasPrev: en.hasPrev, Prev: en.prev})
	e.log("DECIDE insert id=%q hasPrev=%v prev=%q", row.ID, en.hasPrev, en.prev)

	if succ != nil {
		// 后继的新前驱必然是新插入行本身：新状态恒为 HasPrev=true、Prev=新行取值。
		if !succ.hasPrev || succ.prev != row.Value {
			oldHas, oldPrev := succ.hasPrev, succ.prev
			succ.hasPrev, succ.prev = true, row.Value
			changes = append(changes, Change{Kind: ChangeUpdate, Partition: row.Partition,
				ID: succ.row.ID, SortKey: succ.row.SortKey, Value: succ.row.Value,
				HasPrev: succ.hasPrev, Prev: succ.prev})
			e.log("DECIDE update successor id=%q hasPrev %v->%v prev %q->%q",
				succ.row.ID, oldHas, succ.hasPrev, oldPrev, succ.prev)
		} else {
			e.log("DECIDE successor id=%q prev unchanged (hasPrev=%v prev=%q), no entry",
				succ.row.ID, succ.hasPrev, succ.prev)
		}
	}
	e.log("OUTPUT %d change(s)", len(changes))
	return changes, nil
}

// Delete 删除一行，返回本次按固定顺序排列的变更日志：
//  1. 先输出被删行（delete）；
//  2. 若紧邻后继的前驱取值因此改变（含 空<->非空），再输出该后继的 update；
//     前驱取值未变则后继不输出任何条目。
//
// 删除不存在的标识（或非法入参）整体拒绝，不留任何痕迹。
func (e *Engine) Delete(partition, id string) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.log("INPUT  delete partition=%q id=%q", partition, id)

	if partition == "" {
		err := reject("delete", partition, id, ReasonEmptyPartition, "partition name must not be empty")
		e.log("DECIDE reject reason=%s detail=%q (state unchanged)", err.Reason, err.Detail)
		return nil, err
	}
	if id == "" {
		err := reject("delete", partition, id, ReasonEmptyID, "row id must not be empty")
		e.log("DECIDE reject reason=%s detail=%q (state unchanged)", err.Reason, err.Detail)
		return nil, err
	}
	rows := e.parts[partition]
	en := rows[id]
	if rows == nil || en == nil {
		err := reject("delete", partition, id, ReasonMissingID, "id does not exist in partition")
		e.log("DECIDE reject reason=%s detail=%q (state unchanged)", err.Reason, err.Detail)
		return nil, err
	}

	ordered := orderedEntries(rows)
	idx := sort.Search(len(ordered), func(i int) bool {
		return !lessKey(ordered[i].row.SortKey, ordered[i].row.ID, en.row.SortKey, en.row.ID)
	})
	var pred, succ *entry
	if idx > 0 {
		pred = ordered[idx-1]
	}
	if idx+1 < len(ordered) {
		succ = ordered[idx+1]
	}

	changes := make([]Change, 0, 2)
	changes = append(changes, Change{Kind: ChangeDelete, Partition: partition, ID: id,
		SortKey: en.row.SortKey, Value: en.row.Value})
	e.log("DECIDE delete id=%q", id)

	if succ != nil {
		newHas, newPrev := false, ""
		if pred != nil {
			newHas, newPrev = true, pred.row.Value
		}
		if succ.hasPrev != newHas || (newHas && succ.prev != newPrev) {
			oldHas, oldPrev := succ.hasPrev, succ.prev
			succ.hasPrev, succ.prev = newHas, newPrev
			changes = append(changes, Change{Kind: ChangeUpdate, Partition: partition,
				ID: succ.row.ID, SortKey: succ.row.SortKey, Value: succ.row.Value,
				HasPrev: newHas, Prev: newPrev})
			e.log("DECIDE update successor id=%q hasPrev %v->%v prev %q->%q",
				succ.row.ID, oldHas, newHas, oldPrev, newPrev)
		} else {
			e.log("DECIDE successor id=%q prev unchanged (hasPrev=%v prev=%q), no entry",
				succ.row.ID, succ.hasPrev, succ.prev)
		}
	}

	delete(rows, id)
	e.total--
	if len(rows) == 0 {
		delete(e.parts, partition)
	}
	e.log("OUTPUT %d change(s)", len(changes))
	return changes, nil
}

// View 返回指定分区内按 (SortKey, ID) 升序排列的当前视图；分区不存在时返回 nil。
func (e *Engine) View(partition string) []RowView {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.viewLocked(partition)
}

func (e *Engine) viewLocked(partition string) []RowView {
	rows := e.parts[partition]
	if rows == nil {
		return nil
	}
	out := make([]RowView, 0, len(rows))
	for _, en := range orderedEntries(rows) {
		out = append(out, RowView{Row: en.row, HasPrev: en.hasPrev, Prev: en.prev})
	}
	return out
}

// Snapshot 返回所有分区当前视图的一致快照；map 键的遍历顺序不做承诺，
// 每个分区内的行均按 (SortKey, ID) 升序排列。
func (e *Engine) Snapshot() map[string][]RowView {
	e.mu.RLock()
	defer e.mu.RUnlock()
	snap := make(map[string][]RowView, len(e.parts))
	for part := range e.parts {
		snap[part] = e.viewLocked(part)
	}
	return snap
}

// TotalRows 返回当前引擎内的行数。
func (e *Engine) TotalRows() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.total
}
