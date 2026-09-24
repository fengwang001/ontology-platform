// Package cbatch 按批归并部分列更新事件：校验、合并、提交到内存表；依赖 pcol。
package cbatch

import (
	"errors"
	"sync"

	"ontology/pcol"
)

// 三类语义错误与 pcol.ErrBadColumn 互不相同，可用 errors.Is 判定。
var (
	ErrKeyNotFound    = errors.New("cbatch: update on missing key")
	ErrKeyExists      = errors.New("cbatch: insert on existing key")
	ErrBeforeMismatch = errors.New("cbatch: before value does not match actual value")
)

// Table 是固定列集合的内存表，并发安全。
type Table struct {
	mu    sync.RWMutex
	known map[string]struct{}
	rows  map[string]map[string]pcol.Value
}

// NewTable 校验列名（非空、不重复）后建表；列非法返回 pcol.ErrBadColumn。
func NewTable(cols []string) (*Table, error) {
	known := map[string]struct{}{}
	for _, c := range cols {
		if c == "" {
			return nil, pcol.ErrBadColumn
		}
		if _, dup := known[c]; dup {
			return nil, pcol.ErrBadColumn
		}
		known[c] = struct{}{}
	}
	return &Table{known: known, rows: map[string]map[string]pcol.Value{}}, nil
}

// Apply 校验并归并整批，返回合并后输出；任何一条被拒则整批不生效（pending 丢弃，t.rows 不触碰）。
func (t *Table) Apply(batch []pcol.Event) ([]pcol.Event, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	pending := map[string]map[string]pcol.Value{} // 批内影子行：朴素逐条推进
	mergers := map[string]*pcol.Merger{}
	order := []string{}

	for _, e := range batch {
		_, started := pending[e.Key]
		_, inTable := t.rows[e.Key]
		if e.Kind == pcol.KindInsert {
			if started || inTable {
				return nil, ErrKeyExists
			}
			mg, err := pcol.NewMerger(e, t.known)
			if err != nil {
				return nil, err
			}
			mergers[e.Key] = mg
			order = append(order, e.Key)
			pending[e.Key] = cloneRow(e.Set)
			continue
		}
		if e.Kind != pcol.KindUpdate {
			return nil, pcol.ErrBadColumn
		}

		mg, seen := mergers[e.Key]
		if !seen {
			if !inTable {
				return nil, ErrKeyNotFound
			}
			g, err := pcol.NewMerger(e, t.known) // 先格式校验
			if err != nil {
				return nil, err
			}
			mg = g
			pending[e.Key] = cloneRow(t.rows[e.Key]) // 影子从真实行起步
			mergers[e.Key] = mg
			order = append(order, e.Key)
		} else if err := mg.Merge(e); err != nil {
			return nil, err
		}

		cur := pending[e.Key]
		for c, b := range e.Before { // Before 必须等于该事件之前的实际值（首条=真实行，后续=影子行）
			if !cur[c].Equal(b) {
				return nil, ErrBeforeMismatch
			}
		}
		for c, x := range e.Set {
			cur[c] = x
		}
	}

	out := make([]pcol.Event, 0, len(order))
	for _, k := range order {
		if ev, ok := mergers[k].Result(k); ok {
			out = append(out, ev)
		}
	}
	for k, r := range pending { // 全部校验通过后才提交
		t.rows[k] = r
	}
	return out, nil
}

// Row 返回某行的副本；不存在时 ok=false。
func (t *Table) Row(key string) (map[string]pcol.Value, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	r, ok := t.rows[key]
	if !ok {
		return nil, false
	}
	return cloneRow(r), true
}

func cloneRow(in map[string]pcol.Value) map[string]pcol.Value {
	out := make(map[string]pcol.Value, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
