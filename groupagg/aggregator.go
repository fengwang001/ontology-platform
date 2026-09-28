package groupagg

import (
	"io"
	"sort"
	"sync"
)

// Aggregator 是增量分组聚合器，供并发读写使用。
//
// 不变量（仅针对已提交的批次）：
//   - 每个分组的 Sum/Count 恒等于该组当前所有行的值之和与行数；
//   - groups 中只保留 Count > 0 的分组，Count 归 0 立即删除；
//   - changelog 中条目的 Seq 全局单调递增，下游按序应用即可重建视图。
type Aggregator struct {
	mu        sync.RWMutex
	rows      map[string]Row
	groups    map[string]GroupAgg
	changelog []ChangeEntry
	seq       int64
	maxGroups int // <=0 表示不限制

	logger *Logger
}

// New 创建聚合器。maxGroups 为允许同时存在的非空分组数上限（<=0 表示不限制）。
// w 用于诊断日志（打印输入、输出条目与判定依据），可为 nil。
func New(maxGroups int, w io.Writer) *Aggregator {
	return &Aggregator{
		rows:      make(map[string]Row),
		groups:    make(map[string]GroupAgg),
		maxGroups: maxGroups,
		logger:    NewLogger(w),
	}
}

// Apply 原子地应用一个批次。批次内任一操作非法则整批拒绝：
// 行表、聚合与已产生的变更日志均保持不变，返回 *RejectError 说明原因。
func (a *Aggregator) Apply(ops []Op) (result *BatchResult, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 防御性兜底：applyOne 中的存在性断言若被触发，转成批次拒绝。
	defer func() {
		if r := recover(); r != nil {
			if re, ok := r.(*RejectError); ok {
				result = nil
				err = re
				return
			}
			panic(r)
		}
	}()

	// 在副本上模拟整批，只有全部合法才提交，保证被拒绝批次零副作用。
	rows := make(map[string]Row, len(a.rows)+len(ops))
	for id, r := range a.rows {
		rows[id] = r
	}
	groups := make(map[string]GroupAgg, len(a.groups)+2)
	for g, agg := range a.groups {
		groups[g] = agg
	}
	seq := a.seq

	result = &BatchResult{Reports: make([]OpReport, 0, len(ops))}
	entries := make([]ChangeEntry, 0)

	for i, op := range ops {
		if err := validateOp(op); err != nil {
			err.OpIndex = i
			a.logger.LogRejected(ops, err)
			return nil, err
		}
		if err := checkExistence(rows, op); err != nil {
			err.OpIndex = i
			a.logger.LogRejected(ops, err)
			return nil, err
		}

		es, basis := applyOne(rows, groups, op, a.maxGroups, &seq)
		if es == nil {
			err := limitError(op)
			err.OpIndex = i
			a.logger.LogRejected(ops, err)
			return nil, err
		}

		entries = append(entries, es...)
		result.Reports = append(result.Reports, OpReport{Op: op, Entries: es, Basis: basis})
	}

	result.Entries = entries

	// 全部合法：提交副本。
	a.rows = rows
	a.groups = groups
	a.seq = seq
	a.changelog = append(a.changelog, entries...)

	for _, rep := range result.Reports {
		a.logger.LogAccepted(rep)
	}
	return result, nil
}

// validateOp 校验与当前行表无关的畸形输入与存在性约束。
func validateOp(op Op) *RejectError {
	switch op.Kind {
	case OpInsert, OpUpdate, OpDelete:
	default:
		return &RejectError{Reason: ReasonUnknownOp, Message: "unknown op kind"}
	}
	if op.ID == "" {
		return &RejectError{Reason: ReasonEmptyID, Message: "row id must not be empty"}
	}
	switch op.Kind {
	case OpInsert, OpUpdate:
		if op.GroupKey == "" {
			return &RejectError{Reason: ReasonEmptyGroupKey, Message: "group key must not be empty"}
		}
	}
	return nil
}

// checkExistence 在模拟行表上检查行 ID 的存在性约束：
// Insert 不允许重复，Update/Delete 要求行存在。
func checkExistence(rows map[string]Row, op Op) *RejectError {
	_, exists := rows[op.ID]
	switch op.Kind {
	case OpInsert:
		if exists {
			return &RejectError{Reason: ReasonDuplicateInsert, Message: "row already exists: " + op.ID}
		}
	case OpUpdate, OpDelete:
		if !exists {
			reason := ReasonUpdateMissing
			msg := "row does not exist: " + op.ID
			if op.Kind == OpDelete {
				reason = ReasonDeleteMissing
			}
			return &RejectError{Reason: reason, Message: msg}
		}
	}
	return nil
}

// applyOne 在副本上执行单个操作并生成该操作的有序变更条目。
// 返回 nil 条目表示触发组数上限。seq 为调用方持有的序号游标，会被推进。
//
// 输出顺序：受影响分组按“旧组在前、新组在后”排列；每个分组内先 Retract
// 其变更前聚合值，再 Put 其变更后聚合值。
func applyOne(rows map[string]Row, groups map[string]GroupAgg, op Op, maxGroups int, seq *int64) ([]ChangeEntry, string) {
	switch op.Kind {
	case OpInsert:
		if _, exists := rows[op.ID]; exists {
			rejectPanic(ReasonDuplicateInsert, "row already exists: "+op.ID)
		}
		g := op.GroupKey
		if maxGroups > 0 {
			if _, exists := groups[g]; !exists && len(groups) >= maxGroups {
				return nil, ""
			}
		}
		old := groups[g] // 不存在时为零值 (0,0)，即“缺席”
		out := emitPair(nil, op.ID, g, old, GroupAgg{Sum: old.Sum + op.Value, Count: old.Count + 1}, seq)
		groups[g] = GroupAgg{Sum: old.Sum + op.Value, Count: old.Count + 1}
		rows[op.ID] = Row{ID: op.ID, GroupKey: g, Value: op.Value}
		return out, basisInsert(g, old, groups[g])

	case OpUpdate:
		oldRow, exists := rows[op.ID]
		if !exists {
			rejectPanic(ReasonUpdateMissing, "row does not exist: "+op.ID)
		}
		oldG, newG := oldRow.GroupKey, op.GroupKey
		if oldG != newG {
			// 预估非空分组数的净变化：新组不存在则 +1；旧组仅剩该行则 -1。
			if maxGroups > 0 {
				delta := 0
				if _, exists := groups[newG]; !exists {
					delta++
				}
				if groups[oldG].Count == 1 {
					delta--
				}
				if len(groups)+delta > maxGroups {
					return nil, ""
				}
			}
			out := make([]ChangeEntry, 0, 4)
			// 旧组：撤回并减去旧值。
			oldAgg := groups[oldG]
			afterOld := GroupAgg{Sum: oldAgg.Sum - oldRow.Value, Count: oldAgg.Count - 1}
			out = emitPair(out, op.ID, oldG, oldAgg, afterOld, seq)
			if afterOld.Count == 0 {
				delete(groups, oldG)
			} else {
				groups[oldG] = afterOld
			}
			// 新组：撤回（可能缺席）并加入新值。
			newAgg := groups[newG]
			afterNew := GroupAgg{Sum: newAgg.Sum + op.Value, Count: newAgg.Count + 1}
			out = emitPair(out, op.ID, newG, newAgg, afterNew, seq)
			groups[newG] = afterNew
			rows[op.ID] = Row{ID: op.ID, GroupKey: newG, Value: op.Value}
			return out, basisRekey(oldG, newG, oldAgg, afterOld, newAgg, afterNew)
		}
		// 同组更新：仅求和发生净变化。
		g := oldG
		oldAgg := groups[g]
		after := GroupAgg{Sum: oldAgg.Sum - oldRow.Value + op.Value, Count: oldAgg.Count}
		out := emitPair(nil, op.ID, g, oldAgg, after, seq)
		groups[g] = after
		rows[op.ID] = Row{ID: op.ID, GroupKey: g, Value: op.Value}
		return out, basisSameGroup(g, oldRow.Value, op.Value, oldAgg, after)

	case OpDelete:
		oldRow, exists := rows[op.ID]
		if !exists {
			rejectPanic(ReasonDeleteMissing, "row does not exist: "+op.ID)
		}
		g := oldRow.GroupKey
		oldAgg := groups[g]
		after := GroupAgg{Sum: oldAgg.Sum - oldRow.Value, Count: oldAgg.Count - 1}
		out := emitPair(nil, op.ID, g, oldAgg, after, seq)
		if after.Count == 0 {
			delete(groups, g) // 计数归零：组从视图消失；Put(0,0) 即墓碑
		} else {
			groups[g] = after
		}
		delete(rows, op.ID)
		return out, basisDelete(g, oldAgg, after)
	}
	return nil, ""
}

// emitPair 为单个受影响分组生成“先撤回旧值、再写入新值”的两条日志。
func emitPair(out []ChangeEntry, rowID, g string, old, neu GroupAgg, seq *int64) []ChangeEntry {
	*seq++
	out = append(out, ChangeEntry{Seq: *seq, RowID: rowID, Kind: ChangeRetract, Group: g, Sum: old.Sum, Count: old.Count})
	*seq++
	out = append(out, ChangeEntry{Seq: *seq, RowID: rowID, Kind: ChangePut, Group: g, Sum: neu.Sum, Count: neu.Count})
	return out
}

// rejectPanic 用 panic 跨越 applyOne 表达存在性校验失败，由 Apply 捕获并转成拒绝。
// （存在性在 Apply 的预校验中同样检查；这里的防御性分支正常不会触发。）
func rejectPanic(r RejectReason, msg string) {
	panic(&RejectError{Reason: r, Message: msg})
}

func limitError(op Op) *RejectError {
	return &RejectError{Reason: ReasonTooManyGroups, Message: "group count would exceed limit for op on row: " + op.ID}
}

// Snapshot 返回当前所有计数大于 0 的分组聚合副本，可与并发中的写操作安全并发。
func (a *Aggregator) Snapshot() map[string]GroupAgg {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[string]GroupAgg, len(a.groups))
	for g, agg := range a.groups {
		if agg.Count > 0 {
			out[g] = agg
		}
	}
	return out
}

// View 按分组键升序返回当前视图，用于确定性比对与展示。
func (a *Aggregator) View() []GroupView {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]GroupView, 0, len(a.groups))
	for g, agg := range a.groups {
		if agg.Count <= 0 {
			continue
		}
		out = append(out, GroupView{Group: g, Sum: agg.Sum, Count: agg.Count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}

// Rows 返回当前行表的副本。
func (a *Aggregator) Rows() map[string]Row {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[string]Row, len(a.rows))
	for id, r := range a.rows {
		out[id] = r
	}
	return out
}

// Changelog 返回迄今已提交变更日志的副本（Seq 从 1 开始单调递增）。
func (a *Aggregator) Changelog() []ChangeEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]ChangeEntry, len(a.changelog))
	copy(out, a.changelog)
	return out
}
