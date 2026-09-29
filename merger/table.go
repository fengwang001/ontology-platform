package merger

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// EventType 为插入或更新。
type EventType string

const (
	Insert EventType = "insert"
	Update EventType = "update"
)

// Event 表示一条部分列更新事件。
//
// Columns 中存在的列表示“显式给出”（可为显式空值），
// 不存在的列表示“缺席”。Before 仅对更新有效，其键集合必须与
// Columns 的键集合完全相同，值为变更前镜像。
type Event struct {
	Type    EventType
	Key     string
	Columns map[string]ColumnValue
	Before  map[string]ColumnValue
}

// MergedEvent 为一个主键合并后的至多一条输出。
type MergedEvent struct {
	Type    EventType
	Key     string
	Order   int
	Columns map[string]ColumnValue
	Before  map[string]ColumnValue
}

// CommitResult 为一批事件的提交结果。
type CommitResult struct {
	Outputs []MergedEvent
	Applied int
	Skipped map[string]bool
}

// Table 为并发安全的内存表：行查询、自检可与提交并发。
type Table struct {
	mu      sync.RWMutex
	columns []string
	rows    map[string]map[string]ColumnValue
	seq     uint64
	log     io.Writer
}

// NewTable 创建一个列顺序固定的表。
func NewTable(columns []string, log io.Writer) *Table {
	seen := make(map[string]bool, len(columns))
	ordered := make([]string, 0, len(columns))
	for _, c := range columns {
		if c == "" || seen[c] {
			panic(fmt.Sprintf("merger: invalid column definition %q", c))
		}
		seen[c] = true
		ordered = append(ordered, c)
	}
	return &Table{columns: ordered, rows: map[string]map[string]ColumnValue{}, log: log}
}

// Commit 校验并合并整批事件：任何一条非法则整批拒绝且不留痕。
func (t *Table) Commit(events []Event) (*CommitResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.commitLocked(events)
}

// Get 返回某键当前行的拷贝；不存在返回 nil, false。可与 Commit 并发。
func (t *Table) Get(key string) (map[string]ColumnValue, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	row, ok := t.rows[key]
	if !ok {
		return nil, false
	}
	return cloneRow(row), true
}

// Snapshot 返回整表快照的深拷贝。可与 Commit 并发。
func (t *Table) Snapshot() map[string]map[string]ColumnValue {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]map[string]ColumnValue, len(t.rows))
	for k, row := range t.rows {
		out[k] = cloneRow(row)
	}
	return out
}

// SelfCheck 校验表内每行都包含且仅包含全部列且值合法。
func (t *Table) SelfCheck() error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	want := make(map[string]bool, len(t.columns))
	for _, c := range t.columns {
		want[c] = true
	}
	keys := make([]string, 0, len(t.rows))
	for k := range t.rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		row := t.rows[k]
		if len(row) != len(want) {
			return fmt.Errorf("self-check: key %q has %d columns, want %d", k, len(row), len(want))
		}
		for _, c := range t.columns {
			v, ok := row[c]
			if !ok {
				return fmt.Errorf("self-check: key %q missing column %q", k, c)
			}
			if !v.Present() {
				return fmt.Errorf("self-check: key %q column %q holds an absent value", k, c)
			}
		}
	}
	return nil
}

func cloneRow(row map[string]ColumnValue) map[string]ColumnValue {
	out := make(map[string]ColumnValue, len(row))
	for c, v := range row {
		out[c] = v
	}
	return out
}
