// Package cbatch 按批处理事件：按 Key 归并、对照当前表校验、把合并结果应用到表。
package cbatch

import "ontology/pcol"

// Table 是进程内存中的表：固定列集合 + 按主键的行。
type Table struct {
	cols map[string]bool
	rows map[string]map[string]pcol.Value
}

// NewTable 构造表；列名为空或重复返回 ErrBadColumn。
func NewTable(cols []string) (*Table, error) {
	if len(cols) == 0 {
		return nil, pcol.ErrBadColumn
	}
	seen := make(map[string]bool, len(cols))
	for _, c := range cols {
		if c == "" || seen[c] {
			return nil, pcol.ErrBadColumn
		}
		seen[c] = true
	}
	return &Table{cols: seen, rows: map[string]map[string]pcol.Value{}}, nil
}

// Row 返回一行的副本。
func (t *Table) Row(key string) (map[string]pcol.Value, bool) {
	r, ok := t.rows[key]
	if !ok {
		return nil, false
	}
	return clone(r), true
}

func clone(m map[string]pcol.Value) map[string]pcol.Value {
	if m == nil {
		return nil
	}
	out := make(map[string]pcol.Value, len(m))
	for c, v := range m {
		out[c] = v
	}
	return out
}

// ApplyBatch 校验并合并整批，返回按首现顺序排列的合并结果并应用到表。
// 任一事件非法则整批失败，表状态不变。
func (t *Table) ApplyBatch(batch []pcol.Event) ([]pcol.Event, error) {
	accs := map[string]pcol.Event{}
	var order []string
	var mg pcol.Merger
	for _, e := range batch {
		if err := pcol.CheckFormat(e, t.cols); err != nil {
			return nil, err
		}
		acc, seen := accs[e.Key]
		row, inTable := t.rows[e.Key]
		switch e.Kind {
		case pcol.Insert:
			if seen || inTable {
				return nil, pcol.ErrKeyExists
			}
		case pcol.Update:
			if !seen && !inTable {
				return nil, pcol.ErrNoSuchKey
			}
			for c, bv := range e.Before {
				cur, ok := acc.Set[c]
				if !ok {
					cur = row[c]
				}
				if cur != bv {
					return nil, pcol.ErrBeforeMismatch
				}
			}
		}
		if !seen {
			// 首条事件也过 Merge：无变化列剔除对单事件批同样生效
			acc = mg.Merge(pcol.Event{Kind: e.Kind, Key: e.Key,
				Set: map[string]pcol.Value{}, Before: map[string]pcol.Value{}}, e)
			order = append(order, e.Key)
		} else {
			acc = mg.Merge(acc, e)
		}
		accs[e.Key] = acc
	}
	// 全部校验通过后才写表：失败不留痕。
	var out []pcol.Event
	for _, k := range order {
		acc := accs[k]
		if acc.Kind == pcol.Update && len(acc.Set) == 0 {
			continue // 剔除后无变化，本批不输出
		}
		out = append(out, acc)
		if acc.Kind == pcol.Insert {
			t.rows[k] = clone(acc.Set)
		} else {
			for c, v := range acc.Set {
				t.rows[k][c] = v
			}
		}
	}
	return out, nil
}
