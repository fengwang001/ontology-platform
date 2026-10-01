package ontology

import (
	"sort"
	"sync"
)

// Table 是行号到键的单键表，至多持有一个事务。
// 所有方法在同一把互斥锁下串行执行，并发调用等价于某个串行顺序。
type Table struct {
	mu sync.Mutex

	deferrable        bool
	initiallyDeferred bool

	committed map[string]Key
	txn       *txnState
}

type txnState struct {
	rows    map[string]Key
	touched map[string]bool
	mode    Mode
}

// RowKey 是 Keys 返回的行号-键对。
type RowKey struct {
	Row string
	Key Key
}

// NewTable 构造单键表。initiallyDeferred 为真要求 deferrable 为真。
func NewTable(deferrable, initiallyDeferred bool) (*Table, error) {
	if initiallyDeferred && !deferrable {
		return nil, &Error{Reason: ReasonInitiallyDeferredRequiresDeferrable, OpIndex: -1}
	}
	return &Table{
		deferrable:        deferrable,
		initiallyDeferred: initiallyDeferred,
		committed:         map[string]Key{},
	}, nil
}

func initialMode(deferrable, initiallyDeferred bool) Mode {
	if deferrable && initiallyDeferred {
		return DEFERRED
	}
	return IMMEDIATE
}

// Begin 开始事务；同一时刻至多一个事务。
func (t *Table) Begin() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.txn != nil {
		return &Error{Reason: ReasonTransactionActive, OpIndex: -1}
	}
	t.txn = &txnState{
		rows:    cloneRows(t.committed),
		touched: map[string]bool{},
		mode:    initialMode(t.deferrable, t.initiallyDeferred),
	}
	return nil
}

// Apply 作为一条语句顺序执行 ops；失败时整条语句撤销，事务仍有效。
func (t *Table) Apply(ops []Op) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.txn == nil {
		return &Error{Reason: ReasonNoTransaction, OpIndex: -1}
	}

	stm := t.txn
	savedRows := cloneRows(stm.rows)
	savedTouched := cloneTouched(stm.touched)
	stmTouched := map[string]bool{}

	for i, op := range ops {
		fail := func(reason Reason, violatedKey string) error {
			stm.rows = savedRows
			stm.touched = savedTouched
			return &Error{Reason: reason, OpIndex: i, ViolatedKey: violatedKey}
		}
		switch o := op.(type) {
		case InsertOp:
			if o.Row == "" {
				return fail(ReasonEmptyRow, "")
			}
			if _, ok := stm.rows[o.Row]; ok {
				return fail(ReasonRowExists, "")
			}
			stm.rows[o.Row] = o.Key
			if !t.deferrable && duplicatedKey(stm.rows, o.Row) {
				return fail(ReasonUniqueViolation, o.Key.Value)
			}
		case UpdateOp:
			if o.Row == "" {
				return fail(ReasonEmptyRow, "")
			}
			if _, ok := stm.rows[o.Row]; !ok {
				return fail(ReasonRowNotFound, "")
			}
			stm.rows[o.Row] = o.Key
			if !t.deferrable && duplicatedKey(stm.rows, o.Row) {
				return fail(ReasonUniqueViolation, o.Key.Value)
			}
		case DeleteOp:
			if o.Row == "" {
				return fail(ReasonEmptyRow, "")
			}
			if _, ok := stm.rows[o.Row]; !ok {
				return fail(ReasonRowNotFound, "")
			}
			delete(stm.rows, o.Row)
		default:
			return fail(ReasonEmptyRow, "")
		}
		stmTouched[opRow(op)] = true
	}

	// 可延迟约束且当前为即时模式：整条语句执行完后一次检查本语句触碰过的行。
	if t.deferrable && stm.mode == IMMEDIATE {
		if key, ok := minViolation(stm.rows, stmTouched); ok {
			stm.rows = savedRows
			stm.touched = savedTouched
			return &Error{Reason: ReasonUniqueViolation, OpIndex: -1, ViolatedKey: key}
		}
	}

	for row := range stmTouched {
		stm.touched[row] = true
	}
	return nil
}

func opRow(op Op) string {
	switch o := op.(type) {
	case InsertOp:
		return o.Row
	case UpdateOp:
		return o.Row
	case DeleteOp:
		return o.Row
	}
	return ""
}

// SetMode 切换可延迟约束的检查模式；失败时模式与状态不变。
func (t *Table) SetMode(mode Mode) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.txn == nil {
		return &Error{Reason: ReasonNoTransaction, OpIndex: -1}
	}
	if mode != IMMEDIATE && mode != DEFERRED {
		return &Error{Reason: ReasonInvalidMode, OpIndex: -1}
	}
	if !t.deferrable {
		return &Error{Reason: ReasonNotDeferrable, OpIndex: -1}
	}
	stm := t.txn
	if mode == IMMEDIATE && stm.mode == DEFERRED {
		if key, ok := minViolation(stm.rows, stm.touched); ok {
			return &Error{Reason: ReasonUniqueViolation, OpIndex: -1, ViolatedKey: key}
		}
	}
	stm.mode = mode
	return nil
}

// Commit 提交事务；延迟模式下先对事务内触碰过且仍存在的行做一次检查，
// 违例则整个事务回滚（已提交状态不变）并返回违例错误。
func (t *Table) Commit() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.txn == nil {
		return &Error{Reason: ReasonNoTransaction, OpIndex: -1}
	}
	stm := t.txn
	if t.deferrable && stm.mode == DEFERRED {
		if key, ok := minViolation(stm.rows, stm.touched); ok {
			t.txn = nil
			return &Error{Reason: ReasonUniqueViolation, OpIndex: -1, ViolatedKey: key}
		}
	}
	t.committed = stm.rows
	t.txn = nil
	return nil
}

// Rollback 放弃事务，已提交状态不变。
func (t *Table) Rollback() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.txn == nil {
		return &Error{Reason: ReasonNoTransaction, OpIndex: -1}
	}
	t.txn = nil
	return nil
}

// Get 读取行号对应的键；事务内读事务视图，无事务时读已提交状态。
// ok 为假表示行号不存在。
func (t *Table) Get(row string) (Key, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows := t.committed
	if t.txn != nil {
		rows = t.txn.rows
	}
	k, ok := rows[row]
	return k, ok
}

// Keys 按行号字节序返回 (行号, 键) 序列。
func (t *Table) Keys() []RowKey {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows := t.committed
	if t.txn != nil {
		rows = t.txn.rows
	}
	out := make([]RowKey, 0, len(rows))
	for row, key := range rows {
		out = append(out, RowKey{Row: row, Key: key})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Row < out[j].Row })
	return out
}

// duplicatedKey 判断 row 当前的非空键是否与另一行重复。
func duplicatedKey(rows map[string]Key, row string) bool {
	k := rows[row]
	if k.IsNull {
		return false
	}
	for other, otherKey := range rows {
		if other == row || otherKey.IsNull {
			continue
		}
		if otherKey.Value == k.Value {
			return true
		}
	}
	return false
}

// minViolation 在 checked 标记的、仍存在的行中找出违例行，
// 返回其键中字节序最小者。
func minViolation(rows map[string]Key, checked map[string]bool) (string, bool) {
	violated := map[string]bool{}
	counts := map[string]int{}
	for _, key := range rows {
		if key.IsNull {
			continue
		}
		counts[key.Value]++
	}
	for row := range checked {
		key, ok := rows[row]
		if !ok || key.IsNull {
			continue
		}
		if counts[key.Value] > 1 {
			violated[key.Value] = true
		}
	}
	if len(violated) == 0 {
		return "", false
	}
	keys := make([]string, 0, len(violated))
	for k := range violated {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys[0], true
}

func cloneRows(in map[string]Key) map[string]Key {
	out := make(map[string]Key, len(in))
	for row, key := range in {
		out[row] = key
	}
	return out
}

func cloneTouched(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for row := range in {
		out[row] = true
	}
	return out
}
