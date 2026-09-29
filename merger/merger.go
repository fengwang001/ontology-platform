package merger

import (
	"fmt"
	"sort"
	"strings"
)

// keyState 是批内每个主键的纯推演状态，从不回写真实表。
type keyState struct {
	inserted  bool
	effective map[string]ColumnValue // 批内逐条应用后的当前值
	first     map[string]int         // 列 -> 批内首次更新该列的事件下标
	before    map[string]ColumnValue // 列 -> 首次更新时的变更前镜像
	order     int                    // 键在批内首次出现的序号
}

// commitLocked 在已持有写锁的前提下完成“校验+合并+应用”。
// 校验阶段只在局部状态上推演，任何错误都在修改 t.rows 之前返回，失败不留痕。
func (t *Table) commitLocked(events []Event) (*CommitResult, error) {
	t.seq++
	batchID := t.seq
	logf := t.logf(batchID)

	logf("batch #%d begin: %d events", batchID, len(events))
	outputs, err := build(t.columns, t.rows, events, logf)
	if err != nil {
		logf("batch #%d REJECTED: %v — table unchanged", batchID, err)
		return nil, err
	}

	applied := 0
	skipped := map[string]bool{}
	for _, out := range outputs {
		switch out.Type {
		case Insert:
			t.rows[out.Key] = cloneRow(out.Columns)
			applied++
			logf("apply key=%q insert row=%s", out.Key, formatRow(t.columns, out.Columns))
		case Update:
			if len(out.Columns) == 0 {
				skipped[out.Key] = true
				logf("apply key=%q skipped: merged update is a no-op", out.Key)
				continue
			}
			row := t.rows[out.Key]
			for c, v := range out.Columns {
				row[c] = v
			}
			applied++
			logf("apply key=%q update columns=%s", out.Key, formatRow(t.columns, out.Columns))
		}
	}

	logf("batch #%d committed: %d keys applied, %d keys skipped", batchID, applied, len(skipped))
	return &CommitResult{Outputs: outputs, Applied: applied, Skipped: skipped}, nil
}

// build 完成一批事件的校验与纯合并，不触碰表状态。
func build(columns []string, rows map[string]map[string]ColumnValue, events []Event, logf func(string, ...any)) ([]MergedEvent, error) {
	known := make(map[string]bool, len(columns))
	for _, c := range columns {
		known[c] = true
	}

	states := map[string]*keyState{}
	var orderedKeys []string

	for i, ev := range events {
		logf("event[%d] type=%s key=%q columns=%s before=%s",
			i, ev.Type, ev.Key, formatRow(columns, ev.Columns), formatRow(columns, ev.Before))

		if ev.Key == "" {
			return nil, batchError(ErrorEmptyKey, i, ev.Key, "", "event key must not be empty")
		}
		switch ev.Type {
		case Insert, Update:
		default:
			return nil, batchError(ErrorUnknownEventType, i, ev.Key, "",
				fmt.Sprintf("unknown event type %q", ev.Type))
		}

		for c := range ev.Columns {
			if !known[c] {
				return nil, batchError(ErrorUnknownColumn, i, ev.Key, c,
					"column is not part of the table schema")
			}
		}
		for c := range ev.Before {
			if !known[c] {
				return nil, batchError(ErrorUnknownColumn, i, ev.Key, c,
					"before-image column is not part of the table schema")
			}
		}

		st, seen := states[ev.Key]
		if !seen {
			st = &keyState{
				effective: map[string]ColumnValue{},
				first:     map[string]int{},
				before:    map[string]ColumnValue{},
				order:     len(orderedKeys),
			}
			orderedKeys = append(orderedKeys, ev.Key)
			if base, ok := rows[ev.Key]; ok {
				for c, v := range base {
					st.effective[c] = v
				}
			}
			states[ev.Key] = st
		}

		switch ev.Type {
		case Insert:
			if _, exists := rows[ev.Key]; exists {
				return nil, batchError(ErrorKeyAlreadyExists, i, ev.Key, "",
					"key already exists in the table")
			}
			if st.inserted {
				return nil, batchError(ErrorKeyAlreadyExists, i, ev.Key, "",
					"key already inserted earlier in the same batch")
			}
			if len(ev.Columns) != len(columns) {
				var missing []string
				for _, c := range columns {
					if _, ok := ev.Columns[c]; !ok {
						missing = append(missing, c)
					}
				}
				return nil, batchError(ErrorInsertMissingColumn, i, ev.Key,
					firstOrEmpty(missing),
					fmt.Sprintf("insert must provide all %d columns; missing %v", len(columns), missing))
			}
			st.inserted = true
			for c, v := range ev.Columns {
				st.effective[c] = v
			}
			logf("event[%d] key=%q accepted as insert", i, ev.Key)

		case Update:
			if _, exists := rows[ev.Key]; !exists && !st.inserted {
				return nil, batchError(ErrorKeyNotFound, i, ev.Key, "",
					"update targets a key that exists neither in the table nor as a batch insert")
			}
			if len(ev.Columns) == 0 {
				return nil, batchError(ErrorEmptyUpdate, i, ev.Key, "",
					"update must contain at least one changed column")
			}
			if !sameKeys(ev.Columns, ev.Before) {
				missing, extra := diffKeys(ev.Columns, ev.Before)
				return nil, batchError(ErrorChangeMirrorColumnMismatch, i, ev.Key,
					firstOrEmpty(append(append([]string{}, missing...), extra...)),
					fmt.Sprintf("change columns and before-image columns differ: missing in before=%v, extra in before=%v",
						missing, extra))
			}

			touched := sortedKeys(ev.Columns)
			for _, c := range touched {
				firstAt, isFirst := st.first[c]
				if isFirst {
					prev := st.effective[c]
					if !prev.Equal(ev.Before[c]) {
						return nil, batchError(ErrorBeforeImageMismatch, i, ev.Key, c,
							fmt.Sprintf("before-image %s does not match prior event value %s (column first changed at event %d)",
								ev.Before[c], prev, firstAt))
					}
					logf("event[%d] key=%q column=%q repeat update: before=%s matches prior value",
						i, ev.Key, c, ev.Before[c])
				} else {
					actual := st.effective[c]
					if !actual.Equal(ev.Before[c]) {
						return nil, batchError(ErrorBeforeImageMismatch, i, ev.Key, c,
							fmt.Sprintf("before-image %s does not match actual value %s at first update",
								ev.Before[c], actual))
					}
					st.first[c] = i
					st.before[c] = ev.Before[c]
					logf("event[%d] key=%q column=%q first update: before=%s matches actual",
						i, ev.Key, c, ev.Before[c])
				}
				st.effective[c] = ev.Columns[c]
			}
			logf("event[%d] key=%q accepted as update", i, ev.Key)
		}
	}

	outputs := make([]MergedEvent, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		st := states[key]
		if st.inserted {
			// 插入接更新：结果仍为插入，给出全部列，不做无变化剔除。
			cols := make(map[string]ColumnValue, len(columns))
			for _, c := range columns {
				cols[c] = st.effective[c]
			}
			outputs = append(outputs, MergedEvent{
				Type:    Insert,
				Key:     key,
				Order:   st.order,
				Columns: cols,
			})
			logf("merged key=%q => insert (all columns, no dropping) %s", key, formatRow(columns, cols))
			continue
		}

		// 更新接更新：列取并集，同列取后到值；剔除最终值与批前原值相同的列。
		base := rows[key]
		changed := map[string]ColumnValue{}
		before := map[string]ColumnValue{}
		for _, c := range sortedKeysInt(st.first) {
			finalValue := st.effective[c]
			if finalValue.Equal(base[c]) {
				logf("merged key=%q column=%q dropped: final %s equals pre-batch value",
					key, c, finalValue)
				continue
			}
			changed[c] = finalValue
			before[c] = st.before[c]
		}
		out := MergedEvent{
			Type:    Update,
			Key:     key,
			Order:   st.order,
			Columns: changed,
			Before:  before,
		}
		outputs = append(outputs, out)
		if len(changed) == 0 {
			logf("merged key=%q => update emptied after dropping: key produces no change", key)
		} else {
			logf("merged key=%q => update columns=%s before=%s",
				key, formatRow(columns, changed), formatRow(columns, before))
		}
	}

	logf("merge produced %d outputs (at most one per key, first-appearance order)", len(outputs))
	return outputs, nil
}

func (t *Table) logf(batchID uint64) func(string, ...any) {
	return func(format string, args ...any) {
		if t.log == nil {
			return
		}
		fmt.Fprintf(t.log, "[batch %d] %s\n", batchID, fmt.Sprintf(format, args...))
	}
}

func sameKeys(a, b map[string]ColumnValue) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func diffKeys(change, before map[string]ColumnValue) (missing, extra []string) {
	for c := range change {
		if _, ok := before[c]; !ok {
			missing = append(missing, c)
		}
	}
	for c := range before {
		if _, ok := change[c]; !ok {
			extra = append(extra, c)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func sortedKeys(m map[string]ColumnValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysInt(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func firstOrEmpty(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[0]
}

// formatRow 按表结构列顺序渲染列值，缺席的列不展示；非法列追加在后面。
func formatRow(columns []string, row map[string]ColumnValue) string {
	if len(row) == 0 {
		return "{}"
	}
	printed := map[string]bool{}
	parts := make([]string, 0, len(row))
	for _, c := range columns {
		if v, ok := row[c]; ok {
			parts = append(parts, c+"="+v.String())
			printed[c] = true
		}
	}
	var rest []string
	for c := range row {
		if !printed[c] {
			rest = append(rest, c)
		}
	}
	sort.Strings(rest)
	for _, c := range rest {
		parts = append(parts, c+"="+row[c].String())
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
