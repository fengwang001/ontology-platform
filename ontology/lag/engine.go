package lag

import (
	"fmt"
	"sync"
)

// DefaultMaxRows 是单引擎允许容纳的最大行数，可用 WithMaxRows 覆盖。
const DefaultMaxRows = 1_000_000

type entry struct {
	row Row
}

// Engine 增量维护各分区内每行的前驱取值（LAG）。
//
// 所有方法均可被多个执行体并发调用；读视图（Rows / Snapshot / Verify）
// 与提交（Insert / Delete）之间通过读写锁保证看到一致状态。
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
func WithMaxRows(n int) Option {
	return func(e *Engine) { e.maxRows = n }
}

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

func lessKey(sk1 int64, id1 string, sk2 int64, id2 string) bool {
	if sk1 != sk2 {
		return sk1 < sk2
	}
	return id1 < id2
}

func (e *Engine) log(format string, args ...any) {
	e.logf(format, args...)
}

// validateRow 校验单条输入行，返回互不相同的拒绝原因。
func validateRow(op string, row Row) *RejectError {
	if row.Partition == "" {
		return reject(op, row, ReasonEmptyPartition, "partition name must not be empty")
	}
	if row.ID == "" {
		return reject(op, row, ReasonEmptyID, "row id must not be empty")
	}
	return nil
}

// neighbor 返回分区内严格小于 (sortKey,id) 的最大行（前驱）与严格大于的最小行（后继）。
// 任一方向不存在时对应返回值为 nil。
func neighbor(rows map[string]*entry, sortKey int64, id string) (pred, succ *entry) {
	for _, en := range rows {
		if lessKey(en.row.SortKey, en.row.ID, sortKey, id) {
			if pred == nil || lessKey(pred.row.SortKey, pred.row.ID, en.row.SortKey, en.row.ID) {
				pred = en
			}
		} else if lessKey(sortKey, id, en.row.SortKey, en.row.ID) {
			if succ == nil || lessKey(en.row.SortKey, en.row.ID, succ.row.SortKey, succ.row.ID) {
				succ = en
			}
		}
	}
	return pred, succ
}

// Insert 插入一行，返回本次按固定顺序排列的变更日志：
// 先输出新行自身（insert，携带其前驱值），若紧邻后继的前驱取值因此改变，
// 再输出该后继行的 update；前驱值未变则不输出后继条目。
//
// 任何校验失败都整体拒绝：不写入行、不改变视图、不产生日志。
func (e *Engine) Insert(row Row) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.log("INPUT  insert partition=%q id=%q sortKey=%d value=%q", row.Partition, row.ID, row.SortKey, row.Value)
	if err := validateRow("insert", row); err != nil {
		e.log("DECIDE reject reason=%s (state unchanged)", err.Reason)
		return nil, err
	}
	if e.total >= e.maxRows {
		err := reject("insert", row, ReasonRowLimitExceeded,
			fmt.Sprintf("total rows %d would exceed limit %d", e.total+1, e.maxRows))
		e.log("DECIDE reject reason=%s (state unchanged)", err.Reason)
		return nil, err
	}
	rows := e.parts[row.Partition]
	if rows != nil {
		if _, ok := rows[row.ID]; ok {
			err := reject("insert", row, ReasonDuplicateID, "id already exists in partition")
			e.log("DECIDE reject reason=%s (state unchanged)", err.Reason)
			return nil, err
		}
	}

	if rows == nil {
		rows = map[string]*entry{}
		e.parts[row.Partition] = rows
	}
	en := &entry{row: row}
	rows[row.ID] = en
	e.total++

	pred, succ := neighbor(rows, row.SortKey, row.ID)

	changes := make([]Change, 0, 2)
	ins := Change{Kind: ChangeInsert, Partition: row.Partition, ID: row.ID, SortKey: row.SortKey, Value: row.Value}
	if pred != nil {
		ins.HasPrev, ins.Prev = true, pred.row.Value
	}
	changes = append(changes, ins)
	e.log("DECIDE insert %q prev_null=%v prev=%q", row.ID, pred == nil, ins.Prev)

	if succ != nil {
		// 后继旧前驱是 pred（或空），新前驱是新插入行；取值不同才输出修正。
		if pred == nil || pred.row.Value != row.Value {
			up := Change{Kind: ChangeUpdate, Partition: row.Partition, ID: succ.row.ID,
				SortKey: succ.row.SortKey, Value: succ.row.Value, HasPrev: true, Prev: row.Value}
			changes = append(changes, up)
			e.log("DECIDE update successor %q prev -> %q", succ.row.ID, row.Value)
		} else {
			e.log("DECIDE successor %q prev value unchanged (%q), no entry", succ.row.ID, row.Value)
		}
	}
	e.log("OUTPUT %d change(s)", len(changes))
	return changes, nil
}

// Delete 删除一行，返回本次按固定顺序排列的变更日志：
// 先输出被删行（delete），若紧邻后继的前驱取值因此改变，再输出该后继行的
// update；前驱值未变则不输出后继条目。
//
// 删除不存在的标识（或非法入参）整体拒绝，不留任何痕迹。
func (e *Engine) Delete(partition, id string) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.log("INPUT  delete partition=%q id=%q", partition, id)
	key := Row{Partition: partition, ID: id}
	if partition == "" {
		err := reject("delete", key, ReasonEmptyPartition, "partition name must not be empty")
		e.log("DECIDE reject reason=%s (state unchanged)", err.Reason)
		return nil, err
	}
	if id == "" {
		err := reject("delete", key, ReasonEmptyID, "row id must not be empty")
		e.log("DECIDE reject reason=%s (state unchanged)", err.Reason)
		return nil, err
	}
	rows := e.parts[partition]
	en := rows[id]
	if rows == nil || en == nil {
		err := reject("delete", key, ReasonMissingID, "id does not exist in partition")
		e.log("DECIDE reject reason=%s (state unchanged)", err.Reason)
		return nil, err
	}

	pred, succ := neighbor(rows, en.row.SortKey, en.row.ID)

	changes := make([]Change, 0, 2)
	changes = append(changes, Change{Kind: ChangeDelete, Partition: partition, ID: id,
		SortKey: en.row.SortKey, Value: en.row.Value})
	e.log("DECIDE delete %q", id)

	if succ != nil {
		newHasPrev := pred != nil
		newPrev := ""
		if pred != nil {
			newPrev = pred.row.Value
		}
		// 后继旧前驱是被删行；新前驱值不同（含 空<->非空）才输出修正。
		if !newHasPrev || newPrev != en.row.Value {
			up := Change{Kind: ChangeUpdate, Partition: partition, ID: succ.row.ID,
				SortKey: succ.row.SortKey, Value: succ.row.Value, HasPrev: newHasPrev, Prev: newPrev}
			changes = append(changes, up)
			e.log("DECIDE update successor %q prev -> null=%v %q", succ.row.ID, !newHasPrev, newPrev)
		} else {
			e.log("DECIDE successor %q prev value unchanged (%q), no entry", succ.row.ID, newPrev)
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

// TotalRows 返回当前引擎内的行数。
func (e *Engine) TotalRows() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.total
}
