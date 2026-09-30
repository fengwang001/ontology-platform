// Package lag 提供窗口函数 LAG（取前驱值）的增量维护。
//
// 行按分区（Partition）分组，分区内按 (SortKey 升序, ID 升序) 排序。
// 某行的前驱值为同分区内紧邻其前那一行的 Value；分区首行的前驱值为
// nil（空），与数值 0 严格区分。不同分区互不影响。
//
// 通过 Apply 提交插入/删除变更流，返回确定顺序的变更日志；
// View 返回当前每行的前驱值快照；SelfCheck 校验增量视图与批量重算一致。
// 所有导出方法均可被多个执行体并发调用。
package lag

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 输入校验错误类别，互不相同，可用 errors.Is 区分。
var (
	// ErrEmptyID 表示行标识为空。
	ErrEmptyID = errors.New("lag: empty row id")
	// ErrDuplicateID 表示插入的行标识已存在（含同批内重复）。
	ErrDuplicateID = errors.New("lag: duplicate row id")
	// ErrRowNotFound 表示删除的行标识不存在。
	ErrRowNotFound = errors.New("lag: row id not found")
	// ErrEmptyPartition 表示分区名为空。
	ErrEmptyPartition = errors.New("lag: empty partition name")
	// ErrTooManyRows 表示提交后总行数将超过上限。
	ErrTooManyRows = errors.New("lag: row count limit exceeded")
)

// ChangeType 是变更类型：插入或删除。
type ChangeType int

const (
	// ChangeInsert 插入一行。
	ChangeInsert ChangeType = iota
	// ChangeDelete 按标识删除一行（仅使用 Row.ID）。
	ChangeDelete
)

// Row 是一行数据。SortKey 为排序键，Value 为被前驱引用的取值。
type Row struct {
	ID        string
	Partition string
	SortKey   int64
	Value     int64
}

// Change 是一条行变更。Delete 仅需填写 Row.ID。
type Change struct {
	Op  ChangeType
	Row Row
}

// Insert 构造一条插入变更。
func Insert(r Row) Change { return Change{Op: ChangeInsert, Row: r} }

// Delete 构造一条删除变更。
func Delete(id string) Change { return Change{Op: ChangeDelete, Row: Row{ID: id}} }

// LogOp 是变更日志条目类型。
type LogOp int

const (
	// LogUpsert 表示一行的前驱值被写入或修正。
	LogUpsert LogOp = iota
	// LogDelete 表示一行被删除。
	LogDelete
)

// LogEntry 是一条变更日志。下游按 Seq 升序应用后，
// 其视图与批量重算结果一致。Prev 为 nil 表示前驱为空（分区首行），
// 与指向 0 的值严格区分。
type LogEntry struct {
	Seq       int64
	Op        LogOp
	ID        string
	Partition string
	Prev      *int64
}

// ViewEntry 是视图中一行的状态：排序键、取值与当前前驱值。
type ViewEntry struct {
	Partition string
	SortKey   int64
	Value     int64
	Prev      *int64
}

// Maintainer 增量维护每行的前驱值。零值不可用，请用 New 构造。
type Maintainer struct {
	mu      sync.RWMutex
	maxRows int
	rows    map[string]Row
	parts   map[string][]Row // 每个分区内按 (SortKey, ID) 升序
	view    map[string]ViewEntry
	seq     int64
}

// New 构造一个 Maintainer。maxRows 为总行数上限（含所有分区），
// 传 0 表示不限制。
func New(maxRows int) *Maintainer {
	return &Maintainer{
		maxRows: maxRows,
		rows:    make(map[string]Row),
		parts:   make(map[string][]Row),
		view:    make(map[string]ViewEntry),
	}
}

// Apply 校验并整体提交一批变更，返回本次产生的变更日志。
//
// 校验失败时返回非 nil 错误且不产生任何日志，行、视图与日志序号
// 均保持不变（失败不留痕）。日志顺序固定：按输入变更顺序逐条处理；
// 插入先输出新行前驱再修正受影响的后续行，删除先输出被删行再修正
// 受影响行；前驱值未变的行不输出条目。
func (m *Maintainer) Apply(changes []Change) ([]LogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validate(changes); err != nil {
		return nil, err
	}

	var entries []LogEntry
	for _, c := range changes {
		switch c.Op {
		case ChangeInsert:
			entries = m.applyInsert(c.Row, entries)
		case ChangeDelete:
			entries = m.applyDelete(c.Row.ID, entries)
		}
	}
	return entries, nil
}

// validate 在不改变任何状态的前提下整批校验变更。
// 用标识集合模拟批内插入/删除的效果，保证同批内先删后插合法、
// 重复插入与删除不存在标识被拒。
func (m *Maintainer) validate(changes []Change) error {
	present := make(map[string]bool, len(m.rows))
	for id := range m.rows {
		present[id] = true
	}
	count := len(m.rows)
	for _, c := range changes {
		id := c.Row.ID
		if id == "" {
			return fmt.Errorf("%w", ErrEmptyID)
		}
		switch c.Op {
		case ChangeInsert:
			if c.Row.Partition == "" {
				return fmt.Errorf("%w: row %q", ErrEmptyPartition, id)
			}
			if present[id] {
				return fmt.Errorf("%w: %q", ErrDuplicateID, id)
			}
			count++
			if m.maxRows > 0 && count > m.maxRows {
				return fmt.Errorf("%w: max %d", ErrTooManyRows, m.maxRows)
			}
			present[id] = true
		case ChangeDelete:
			if !present[id] {
				return fmt.Errorf("%w: %q", ErrRowNotFound, id)
			}
			count--
			present[id] = false
		default:
			return fmt.Errorf("lag: unknown change type %d", c.Op)
		}
	}
	return nil
}

// applyInsert 插入一行：先输出新行的前驱，再修正紧邻其后的行。
func (m *Maintainer) applyInsert(r Row, entries []LogEntry) []LogEntry {
	rows := m.parts[r.Partition]
	pos := sort.Search(len(rows), func(i int) bool {
		return lessRow(r, rows[i])
	})

	var prev *int64
	if pos > 0 {
		prev = int64Ptr(rows[pos-1].Value)
	}

	rows = append(rows, Row{})
	copy(rows[pos+1:], rows[pos:])
	rows[pos] = r
	m.parts[r.Partition] = rows
	m.rows[r.ID] = r
	m.view[r.ID] = ViewEntry{Partition: r.Partition, SortKey: r.SortKey, Value: r.Value, Prev: prev}
	entries = m.emit(entries, LogUpsert, r.ID, r.Partition, prev)

	// 修正后继：其后继的前驱变为 r.Value，仅当取值变化时输出。
	if pos+1 < len(rows) {
		succ := rows[pos+1]
		if old := m.view[succ.ID].Prev; !eqPtr(old, int64Ptr(r.Value)) {
			np := int64Ptr(r.Value)
			e := m.view[succ.ID]
			e.Prev = np
			m.view[succ.ID] = e
			entries = m.emit(entries, LogUpsert, succ.ID, succ.Partition, np)
		}
	}
	return entries
}

// applyDelete 删除一行：先输出被删行，再修正紧邻其后的行。
func (m *Maintainer) applyDelete(id string, entries []LogEntry) []LogEntry {
	r := m.rows[id]
	rows := m.parts[r.Partition]
	pos := sort.Search(len(rows), func(i int) bool {
		return !lessRow(rows[i], r)
	})

	entries = m.emit(entries, LogDelete, id, r.Partition, m.view[id].Prev)

	rows = append(rows[:pos], rows[pos+1:]...)
	if len(rows) == 0 {
		delete(m.parts, r.Partition)
	} else {
		m.parts[r.Partition] = rows
	}
	delete(m.rows, id)
	delete(m.view, id)

	// 修正后继：其后继的前驱变为被删行原来的前驱（可能为空）。
	if pos < len(rows) {
		succ := rows[pos]
		var np *int64
		if pos > 0 {
			np = int64Ptr(rows[pos-1].Value)
		}
		if old := m.view[succ.ID].Prev; !eqPtr(old, np) {
			e := m.view[succ.ID]
			e.Prev = np
			m.view[succ.ID] = e
			entries = m.emit(entries, LogUpsert, succ.ID, succ.Partition, np)
		}
	}
	return entries
}

func (m *Maintainer) emit(entries []LogEntry, op LogOp, id, partition string, prev *int64) []LogEntry {
	m.seq++
	return append(entries, LogEntry{Seq: m.seq, Op: op, ID: id, Partition: partition, Prev: prev})
}

// lessRow 报告 a 是否排在 b 之前：SortKey 升序，并列按 ID 升序。
func lessRow(a, b Row) bool {
	if a.SortKey != b.SortKey {
		return a.SortKey < b.SortKey
	}
	return a.ID < b.ID
}

func int64Ptr(v int64) *int64 { return &v }

func eqPtr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// View 返回当前视图快照（逐标识的前驱值），可并发调用。
func (m *Maintainer) View() map[string]ViewEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]ViewEntry, len(m.view))
	for id, e := range m.view {
		out[id] = cloneEntry(e)
	}
	return out
}

// SelfCheck 校验增量维护的视图与批量重算结果一致，可并发调用。
func (m *Maintainer) SelfCheck() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rows := make([]Row, 0, len(m.rows))
	for _, r := range m.rows {
		rows = append(rows, r)
	}
	want := BatchView(rows)
	if len(want) != len(m.view) {
		return fmt.Errorf("lag: self-check failed: view has %d rows, batch has %d", len(m.view), len(want))
	}
	for id, w := range want {
		got, ok := m.view[id]
		if !ok {
			return fmt.Errorf("lag: self-check failed: row %q missing from view", id)
		}
		if got.Partition != w.Partition || got.SortKey != w.SortKey || got.Value != w.Value || !eqPtr(got.Prev, w.Prev) {
			return fmt.Errorf("lag: self-check failed: row %q view %+v != batch %+v", id, got, w)
		}
	}
	return nil
}

// BatchView 对一组行做批量重算，返回逐标识的前驱值视图。
// 相同行集与插入顺序无关，结果可复现。
func BatchView(rows []Row) map[string]ViewEntry {
	byPart := make(map[string][]Row)
	for _, r := range rows {
		byPart[r.Partition] = append(byPart[r.Partition], r)
	}
	out := make(map[string]ViewEntry, len(rows))
	for _, part := range byPart {
		sort.Slice(part, func(i, j int) bool { return lessRow(part[i], part[j]) })
		for i, r := range part {
			var prev *int64
			if i > 0 {
				prev = int64Ptr(part[i-1].Value)
			}
			out[r.ID] = ViewEntry{Partition: r.Partition, SortKey: r.SortKey, Value: r.Value, Prev: prev}
		}
	}
	return out
}

func cloneEntry(e ViewEntry) ViewEntry {
	if e.Prev != nil {
		e.Prev = int64Ptr(*e.Prev)
	}
	return e
}
