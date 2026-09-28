package ontology

import "sort"

// groupState 是单个分组的聚合状态。计数归零的组会立刻从引擎的 map 中
// 删除，因此 groups 中只保留计数为正的组，len(groups) 即当前活跃组数。
type groupState struct {
	sum   int64
	count int64
}

// engine 是聚合器的无锁内部实现（单 goroutine 状态机）。
// 方法本身不做并发控制，由 Aggregator 通过 actor 循环串行调用。
type engine struct {
	rows      map[string]Row
	groups    map[string]groupState
	log       []LogEntry
	seq       int
	maxGroups int // <= 0 表示不限制
}

// newEngine 创建内部引擎。maxGroups <= 0 表示不限制组数。
func newEngine(maxGroups int) *engine {
	return &engine{
		rows:      make(map[string]Row),
		groups:    make(map[string]groupState),
		maxGroups: maxGroups,
	}
}

// reject 构造指向批次中第 index 条操作的拒绝错误。
func reject(index int, op Op, cause error) error {
	return &RejectError{Index: index, Op: op, Cause: cause}
}

// apply 在状态副本上逐条校验并应用一批操作，全部成功后才原子替换当前状态。
// 任一条非法则返回 *RejectError，行表、聚合与日志均不改变。
func (e *engine) apply(ops []Op) ([]LogEntry, error) {
	// 工作副本：失败时直接丢弃，原状态不受影响。
	rows := make(map[string]Row, len(e.rows)+len(ops))
	for id, r := range e.rows {
		rows[id] = r
	}
	groups := make(map[string]groupState, len(e.groups)+len(ops))
	for g, s := range e.groups {
		groups[g] = s
	}

	entries := make([]LogEntry, 0, len(ops)*2)
	nextSeq := e.seq

	// emit 先按 delta 改动分组状态，再落一条带“应用后快照”的日志。
	emit := func(index int, op Op, group string, kind EntryKind, value, count int64) {
		s := groups[group]
		s.sum += value
		s.count += count
		if s.count == 0 {
			// 组已空：移出 map，视图与组数统计都不再包含它。
			delete(groups, group)
			s = groupState{}
		} else {
			groups[group] = s
		}
		nextSeq++
		entries = append(entries, LogEntry{
			Seq:        nextSeq,
			OpIndex:    index,
			RowID:      op.RowID,
			Group:      group,
			Kind:       kind,
			Value:      value,
			Count:      count,
			SumAfter:   s.sum,
			CountAfter: s.count,
		})
	}

	for i, op := range ops {
		if op.RowID == "" {
			return nil, reject(i, op, ErrEmptyRowKey)
		}

		switch op.Kind {
		case OpInsert:
			if op.Group == "" {
				return nil, reject(i, op, ErrEmptyGroupKey)
			}
			if _, exists := rows[op.RowID]; exists {
				return nil, reject(i, op, ErrDuplicateRow)
			}
			if _, live := groups[op.Group]; !live && e.maxGroups > 0 && len(groups) >= e.maxGroups {
				return nil, reject(i, op, ErrTooManyGroups)
			}
			rows[op.RowID] = Row{Group: op.Group, Value: op.Value}
			emit(i, op, op.Group, EntryAdd, op.Value, 1)

		case OpUpdate:
			if op.Group == "" {
				return nil, reject(i, op, ErrEmptyGroupKey)
			}
			old, exists := rows[op.RowID]
			if !exists {
				return nil, reject(i, op, ErrRowNotFound)
			}
			// 第一步：撤回旧值（旧组）。
			emit(i, op, old.Group, EntryRetract, -old.Value, -1)
			// 第二步：写入新值（新组）。若因改键而产生新组，需检查组数上限。
			if _, live := groups[op.Group]; !live && e.maxGroups > 0 && len(groups) >= e.maxGroups {
				return nil, reject(i, op, ErrTooManyGroups)
			}
			rows[op.RowID] = Row{Group: op.Group, Value: op.Value}
			emit(i, op, op.Group, EntryAdd, op.Value, 1)

		case OpDelete:
			old, exists := rows[op.RowID]
			if !exists {
				return nil, reject(i, op, ErrRowNotFound)
			}
			delete(rows, op.RowID)
			emit(i, op, old.Group, EntryRetract, -old.Value, -1)

		default:
			return nil, reject(i, op, ErrInvalidOp)
		}
	}

	// 全部合法：提交副本。
	e.rows = rows
	e.groups = groups
	e.seq = nextSeq
	e.log = append(e.log, entries...)
	return entries, nil
}

// snapshot 返回当前计数为正的分组视图（按分组键排序）。
func (e *engine) snapshot() []GroupView {
	out := make([]GroupView, 0, len(e.groups))
	for g, s := range e.groups { // map 中只保留 count > 0 的组
		out = append(out, GroupView{Group: g, Sum: s.sum, Count: s.count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}

// log 返回全部已提交日志条目的副本。
func (e *engine) logView() []LogEntry {
	out := make([]LogEntry, len(e.log))
	copy(out, e.log)
	return out
}

// rows 返回行表副本。
func (e *engine) rowsView() map[string]Row {
	out := make(map[string]Row, len(e.rows))
	for id, r := range e.rows {
		out[id] = r
	}
	return out
}
