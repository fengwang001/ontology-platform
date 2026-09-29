package subtotal

import (
	"fmt"
	"sort"
)

// View 返回当前三层状态的一致快照（拷贝，调用方可自由修改返回值）。
func (s *Store) View() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snap := Snapshot{
		Details: make(map[Key2]GroupStat, len(s.details)),
		Sub1s:   make(map[Key1]GroupStat, len(s.sub1s)),
		Total:   s.total,
	}
	for k, v := range s.details {
		snap.Details[k] = v
	}
	for k, v := range s.sub1s {
		snap.Sub1s[k] = v
	}
	return snap
}

// Log 返回已提交变更日志的拷贝；每条已提交增量对应 0~6 条变更，
// 按 seq 排序，其任意“整条增量前缀”重放后都必须自洽。
func (s *Store) Log() []Change {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Change, len(s.log))
	copy(out, s.log)
	return out
}

// Rows 返回当前存活行集的拷贝，便于与批量重算基准对照。
func (s *Store) Rows() []Row {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Row, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, dupRow(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// BatchRecompute 用给定行集从零批量重算三层快照，作为增量结果的对照基准。
// 若出现重复行 ID，以切片中最后出现者为准。
func BatchRecompute(rows []Row) Snapshot {
	latest := make(map[string]Row, len(rows))
	order := make([]string, 0, len(rows))
	for _, r := range rows {
		if _, ok := latest[r.ID]; !ok {
			order = append(order, r.ID)
		}
		latest[r.ID] = dupRow(r)
	}

	snap := Snapshot{
		Details: make(map[Key2]GroupStat),
		Sub1s:   make(map[Key1]GroupStat),
	}
	for _, id := range order {
		r := latest[id]
		k2, k1 := key2Of(r), key1Of(r)
		g := snap.Details[k2]
		g.Count++
		g.Sum += r.Amount
		snap.Details[k2] = g

		sb := snap.Sub1s[k1]
		sb.Count++
		sb.Sum += r.Amount
		snap.Sub1s[k1] = sb

		snap.Total.Count++
		snap.Total.Sum += r.Amount
	}
	return snap
}

// Replay 按序应用变更日志，返回得到的三层快照。
// 计数归零的明细组/小计组被删除；总计为固定占位恒保留；
// Created/Removed 仅作日志注解，不参与重放计算。
func Replay(changes []Change) Snapshot {
	snap := Snapshot{
		Details: make(map[Key2]GroupStat),
		Sub1s:   make(map[Key1]GroupStat),
	}
	for _, c := range changes {
		switch {
		case c.Grand:
			snap.Total.Count += c.CDelta
			snap.Total.Sum += c.Delta
		case c.Layer == LayerDetail:
			a, an := dimVal(c.Dim1)
			b, bn := dimVal(c.Dim2)
			k := Key2{A: a, ANull: an, B: b, BNull: bn}
			g := snap.Details[k]
			g.Count += c.CDelta
			g.Sum += c.Delta
			if g.Count == 0 {
				delete(snap.Details, k)
			} else {
				snap.Details[k] = g
			}
		default:
			v, null := dimVal(c.Dim1)
			k := Key1{V: v, Null: null}
			g := snap.Sub1s[k]
			g.Count += c.CDelta
			g.Sum += c.Delta
			if g.Count == 0 {
				delete(snap.Sub1s, k)
			} else {
				snap.Sub1s[k] = g
			}
		}
	}
	return snap
}

// EqualSnapshot 比较两个快照的三层计数与求和是否完全一致。
func EqualSnapshot(a, b Snapshot) bool {
	if a.Total != b.Total || len(a.Details) != len(b.Details) ||
		len(a.Sub1s) != len(b.Sub1s) {
		return false
	}
	for k, v := range a.Details {
		if v2, ok := b.Details[k]; !ok || v != v2 {
			return false
		}
	}
	for k, v := range a.Sub1s {
		if v2, ok := b.Sub1s[k]; !ok || v != v2 {
			return false
		}
	}
	return true
}

func k1FromK2(k Key2) Key1 { return Key1{V: k.A, Null: k.ANull} }

// SelfCheck 校验内部不变量：
//  1. 任一明细组、小计组计数都大于零（计数为零即删除）；
//  2. 每个第一维小计等于其名下全部明细组之和；
//  3. 总计等于全部明细组之和；
//  4. 完整日志可重放出当前状态；
//  5. 每个整条增量前缀重放后都满足三层自洽（任意前缀自洽）。
func (s *Store) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for k, g := range s.details {
		if g.Count <= 0 {
			return fmt.Errorf("detail group %v has non-positive count %d", k, g.Count)
		}
	}
	for k, g := range s.sub1s {
		if g.Count <= 0 {
			return fmt.Errorf("subtotal group %v has non-positive count %d", k, g.Count)
		}
	}

	byKey1 := make(map[Key1]GroupStat)
	for k2, g := range s.details {
		k1 := k1FromK2(k2)
		agg := byKey1[k1]
		agg.Count += g.Count
		agg.Sum += g.Sum
		byKey1[k1] = agg
	}
	for k1, want := range byKey1 {
		got := s.sub1s[k1]
		if got != want {
			return fmt.Errorf("subtotal %v = %+v, want %+v from details", k1, got, want)
		}
	}
	for k1 := range s.sub1s {
		if _, ok := byKey1[k1]; !ok {
			return fmt.Errorf("subtotal %v exists without any detail group", k1)
		}
	}

	var fromDetails GroupStat
	for _, g := range byKey1 {
		fromDetails.Count += g.Count
		fromDetails.Sum += g.Sum
	}
	if s.total != fromDetails {
		return fmt.Errorf("total = %+v, want %+v from details", s.total, fromDetails)
	}

	if replayed := Replay(s.log); !EqualSnapshot(replayed, s.snapshotLocked()) {
		return fmt.Errorf("full log replay diverges from current state")
	}

	for i := range s.log {
		if !isCommitBoundary(s.log, i) {
			continue
		}
		prefix := Replay(s.log[:i+1])
		if err := consistent(prefix); err != nil {
			return fmt.Errorf("log prefix ending at seq %d inconsistent: %w",
				s.log[i].Seq, err)
		}
	}
	return nil
}

func (s *Store) snapshotLocked() Snapshot {
	snap := Snapshot{
		Details: make(map[Key2]GroupStat, len(s.details)),
		Sub1s:   make(map[Key1]GroupStat, len(s.sub1s)),
		Total:   s.total,
	}
	for k, v := range s.details {
		snap.Details[k] = v
	}
	for k, v := range s.sub1s {
		snap.Sub1s[k] = v
	}
	return snap
}

// isCommitBoundary 判断下标 i 是否为某条增量产生的最后一条变更：
// 每条增量的最后一条变更恒为 LayerTotal；其后要么是日志末尾，
// 要么是下一条增量首条 LayerDetail 变更。
func isCommitBoundary(log []Change, i int) bool {
	if log[i].Layer != LayerTotal {
		return false
	}
	if i == len(log)-1 {
		return true
	}
	return log[i+1].Layer == LayerDetail
}

// consistent 校验一个重放快照三层互相对得上。
func consistent(snap Snapshot) error {
	byKey1 := make(map[Key1]GroupStat)
	for k2, g := range snap.Details {
		if g.Count <= 0 {
			return fmt.Errorf("detail %v non-positive count", k2)
		}
		k1 := k1FromK2(k2)
		agg := byKey1[k1]
		agg.Count += g.Count
		agg.Sum += g.Sum
		byKey1[k1] = agg
	}
	for k1, want := range byKey1 {
		if got := snap.Sub1s[k1]; got != want {
			return fmt.Errorf("subtotal %v = %+v want %+v", k1, got, want)
		}
	}
	for k1, g := range snap.Sub1s {
		if g.Count <= 0 {
			return fmt.Errorf("subtotal %v non-positive count", k1)
		}
		if _, ok := byKey1[k1]; !ok {
			return fmt.Errorf("subtotal %v has no details", k1)
		}
	}
	var total GroupStat
	for _, g := range byKey1 {
		total.Count += g.Count
		total.Sum += g.Sum
	}
	if snap.Total != total {
		return fmt.Errorf("total %+v want %+v", snap.Total, total)
	}
	return nil
}
