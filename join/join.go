package join

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// table 保存一侧表的数据：按 ID 的行表，以及键到 ID 集合的索引。
type table struct {
	rows  map[string]Row
	byKey map[string]map[string]struct{}
}

func newTable() table {
	return table{
		rows:  map[string]Row{},
		byKey: map[string]map[string]struct{}{},
	}
}

func (t *table) clone() table {
	c := table{rows: make(map[string]Row, len(t.rows)), byKey: make(map[string]map[string]struct{}, len(t.byKey))}
	for id, r := range t.rows {
		c.rows[id] = r
	}
	for k, ids := range t.byKey {
		s := make(map[string]struct{}, len(ids))
		for id := range ids {
			s[id] = struct{}{}
		}
		c.byKey[k] = s
	}
	return c
}

func (t *table) contains(id string) bool { _, ok := t.rows[id]; return ok }

func (t *table) put(r Row) {
	t.rows[r.ID] = r
	s := t.byKey[r.Key]
	if s == nil {
		s = map[string]struct{}{}
		t.byKey[r.Key] = s
	}
	s[r.ID] = struct{}{}
}

func (t *table) remove(r Row) {
	delete(t.rows, r.ID)
	if s := t.byKey[r.Key]; s != nil {
		delete(s, r.ID)
		if len(s) == 0 {
			delete(t.byKey, r.Key)
		}
	}
}

// keyIDs 返回某键下按字典序排列的全部 ID。
func (t *table) keyIDs(key string) []string {
	ids := make([]string, 0, len(t.byKey[key]))
	for id := range t.byKey[key] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (t *table) count() int { return len(t.rows) }

// Joiner 是并发安全的增量左外连接组件。零值不可用，必须通过 New 创建。
type Joiner struct {
	mu      sync.RWMutex
	maxRows int
	left    table
	right   table
	log     []Decision
	out     io.Writer
}

// New 创建组件；maxRows <= 0 表示不限左右两表总行数。
func New(maxRows int) *Joiner {
	return &Joiner{
		maxRows: maxRows,
		left:    newTable(),
		right:   newTable(),
		out:     io.Discard,
	}
}

// WithLogWriter 设置判定日志的文本输出位置并返回组件本身，便于链式构造。
func (j *Joiner) WithLogWriter(w io.Writer) *Joiner {
	j.mu.Lock()
	defer j.mu.Unlock()
	if w == nil {
		w = io.Discard
	}
	j.out = w
	return j
}

// MaxRows 返回总行数上限；<=0 表示不限。
func (j *Joiner) MaxRows() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.maxRows
}

// LeftCount 返回左表当前行数。
func (j *Joiner) LeftCount() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.left.count()
}

// RightCount 返回右表当前行数。
func (j *Joiner) RightCount() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.right.count()
}

// Apply 原子地应用一批操作：全部合法才落库；任一条非法则整批拒绝，
// 两表与已产生的结构化判定日志都不发生改变。
func (j *Joiner) Apply(ops []Op) (*Result, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	// 阶段一：在表的副本上干跑，完成全部校验并推导确定性输出。
	dryL, dryR := j.left.clone(), j.right.clone()
	decisions := make([]Decision, 0, len(ops))
	allEntries := make([]Entry, 0)
	for i, op := range ops {
		entries, reason, detail, err := applyOne(&dryL, &dryR, op, j.maxRows, dryL.count()+dryR.count())
		if err != nil {
			re := &RejectError{Index: i, Op: op, Reason: reason}
			j.writeText(Decision{Index: i, Op: op, Accepted: false, Reason: reason, Detail: detail})
			return nil, re
		}
		decisions = append(decisions, Decision{
			Index: i, Op: op, Accepted: true, Detail: detail, Entries: entries,
		})
		allEntries = append(allEntries, entries...)
	}

	// 阶段二：干跑通过，在真实表上重放同样的增删。
	for _, op := range ops {
		t := j.targetTable(op.Side)
		if op.Kind == Insert {
			t.put(op.Row)
		} else {
			t.remove(op.Row)
		}
	}
	for idx := range decisions {
		j.log = append(j.log, decisions[idx])
		j.writeText(decisions[idx])
	}
	return &Result{Entries: allEntries}, nil
}

func (j *Joiner) targetTable(s Side) *table {
	if s == Left {
		return &j.left
	}
	return &j.right
}

// applyOne 校验单条操作并作用于给定表，返回该操作产生的有序变更条目。
func applyOne(l, r *table, op Op, maxRows, totalRows int) ([]Entry, Reason, string, error) {
	if op.Side != Left && op.Side != Right {
		return nil, ReasonUnknownSide, "side 字段非法，必须为 Left 或 Right", errUnknownSide
	}
	if op.Kind != Insert && op.Kind != Delete {
		return nil, ReasonUnknownKind, "kind 字段非法，必须为 Insert 或 Delete", errUnknownKind
	}
	if op.Row.Key == "" {
		return nil, ReasonEmptyKey, "连接键为空，空键行被拒绝", errEmptyKey
	}
	if op.Row.ID == "" {
		return nil, ReasonEmptyID, "行标识为空，空标识行被拒绝", errEmptyID
	}

	t, other := l, r
	if op.Side == Right {
		t, other = r, l
	}

	if op.Kind == Insert {
		if t.contains(op.Row.ID) {
			return nil, ReasonDuplicateID,
				fmt.Sprintf("%s 侧已存在标识 %q，重复插入被拒绝", op.Side, op.Row.ID),
				errDuplicateID
		}
		if maxRows > 0 && totalRows >= maxRows {
			return nil, ReasonRowLimitExceeded,
				fmt.Sprintf("插入将使总行数达到 %d，超过上限 %d", totalRows+1, maxRows),
				errRowLimitExceeded
		}
	} else if !t.contains(op.Row.ID) {
		return nil, ReasonIDNotFound,
			fmt.Sprintf("%s 侧不存在标识 %q，删除不存在的行被拒绝", op.Side, op.Row.ID),
			errIDNotFound
	}

	switch {
	case op.Side == Left && op.Kind == Insert:
		t.put(op.Row)
		entries, d := leftInsertEntries(op.Row, other)
		return entries, "", d, nil
	case op.Side == Left && op.Kind == Delete:
		entries, d := leftDeleteEntries(op.Row, other)
		t.remove(op.Row)
		return entries, "", d, nil
	case op.Side == Right && op.Kind == Insert:
		before := len(t.byKey[op.Row.Key])
		t.put(op.Row)
		entries, d := rightInsertEntries(op.Row, other, before)
		return entries, "", d, nil
	default: // Right, Delete
		leftIDs := other.keyIDs(op.Row.Key)
		remaining := len(t.byKey[op.Row.Key]) - 1
		entries, d := rightDeleteEntries(op.Row, other, t, leftIDs, remaining)
		t.remove(op.Row)
		return entries, "", d, nil
	}
}

// pairedEntry 构造一条配对结果行的变更条目。
func pairedEntry(k Kind, lr, rr Row) Entry {
	return Entry{
		Kind: k, Key: lr.Key, LeftID: lr.ID, LeftValue: lr.Value,
		RightID: rr.ID, RightValue: rr.Value,
	}
}

// paddedEntry 构造一条空填充结果行的变更条目（右侧标识为空）。
func paddedEntry(k Kind, lr Row) Entry {
	return Entry{Kind: k, Key: lr.Key, LeftID: lr.ID, LeftValue: lr.Value}
}

// 左插入：右行按对侧标识字典序配对；无右行时只插入空填充行。
func leftInsertEntries(lr Row, rt *table) ([]Entry, string) {
	ids := rt.keyIDs(lr.Key)
	if len(ids) == 0 {
		return []Entry{paddedEntry(Insert, lr)},
			fmt.Sprintf("左行 %q 插入：键 %q 无同键右行，插入空填充行", lr.ID, lr.Key)
	}
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, pairedEntry(Insert, lr, rt.rows[id]))
	}
	return entries, fmt.Sprintf("左行 %q 插入：键 %q 有 %d 条同键右行，全部配对插入", lr.ID, lr.Key, len(ids))
}

// 左删除：撤回其全部配对行；若以空填充行存在则撤回空填充行。
func leftDeleteEntries(lr Row, rt *table) ([]Entry, string) {
	ids := rt.keyIDs(lr.Key)
	if len(ids) == 0 {
		return []Entry{paddedEntry(Delete, lr)},
			fmt.Sprintf("左行 %q 删除：其以空填充行存在，撤回空填充行", lr.ID)
	}
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, pairedEntry(Delete, lr, rt.rows[id]))
	}
	return entries, fmt.Sprintf("左行 %q 删除：撤回其 %d 条配对行", lr.ID, len(ids))
}

// 右插入：影响所有同键左行。若是该键首条右行，对每个左行先撤回空填充行、
// 再补配对行，两条记录相邻（同一左行）。左行按对侧标识（左标识）字典序处理。
func rightInsertEntries(rr Row, lt *table, rightsBefore int) ([]Entry, string) {
	ids := lt.keyIDs(rr.Key)
	entries := make([]Entry, 0, len(ids)*2)
	for _, id := range ids {
		lr := lt.rows[id]
		if rightsBefore == 0 {
			entries = append(entries, paddedEntry(Delete, lr))
		}
		entries = append(entries, pairedEntry(Insert, lr, rr))
	}
	switch {
	case len(ids) == 0:
		return entries, fmt.Sprintf("右行 %q 插入：键 %q 当前无左行，暂无配对输出", rr.ID, rr.Key)
	case rightsBefore == 0:
		return entries, fmt.Sprintf("右行 %q 插入：键 %q 的首条右行，撤回 %d 条空填充行并补配对行", rr.ID, rr.Key, len(ids))
	default:
		return entries, fmt.Sprintf("右行 %q 插入：与键 %q 的 %d 条左行新增配对", rr.ID, rr.Key, len(ids))
	}
}

// 右删除：撤回相关配对行；若它是该键最后一条右行，为每个同键左行补回空填充行。
func rightDeleteEntries(rr Row, lt, rt *table, leftIDs []string, remaining int) ([]Entry, string) {
	entries := make([]Entry, 0, len(leftIDs)*2)
	for _, id := range leftIDs {
		lr := lt.rows[id]
		entries = append(entries, pairedEntry(Delete, lr, rr))
		if remaining == 0 {
			entries = append(entries, paddedEntry(Insert, lr))
		}
	}
	switch {
	case len(leftIDs) == 0:
		return entries, fmt.Sprintf("右行 %q 删除：键 %q 无左行，仅撤回表数据", rr.ID, rr.Key)
	case remaining == 0:
		return entries, fmt.Sprintf("右行 %q 删除：键 %q 的最后一条右行，撤回配对并为 %d 条左行补回空填充行", rr.ID, rr.Key, len(leftIDs))
	default:
		return entries, fmt.Sprintf("右行 %q 删除：撤回与 %d 条左行的配对，仍有 %d 条同键右行", rr.ID, len(leftIDs), remaining)
	}
}
