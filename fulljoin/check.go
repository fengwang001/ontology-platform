package fulljoin

import "fmt"

// Check 做一致性自检：
//  1. 用两侧原始行批量重算全外连接，与增量维护的视图逐字段比对；
//  2. 从空状态重放全部已提交日志，比对得到同一视图。
//
// 一致时返回 nil。该方法只读，可与 View/Log 并发调用。
func (m *Maintainer) Check() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	incremental := make([]OutRow, 0, len(m.out))
	for o := range m.out {
		incremental = append(incremental, cloneOutRow(o))
	}
	sortOutRows(incremental)

	recomputed := m.recomputeLocked()
	if err := diffViews("batch recompute", recomputed, incremental); err != nil {
		return err
	}

	replayed, err := replay(m.log)
	if err != nil {
		return fmt.Errorf("fulljoin: log replay failed: %w", err)
	}
	if err := diffViews("log replay", replayed, incremental); err != nil {
		return err
	}
	return nil
}

// recomputeLocked 从两侧原始行集合批量重算全外连接。
func (m *Maintainer) recomputeLocked() []OutRow {
	keys := map[string]struct{}{}
	for k := range m.left.byKey {
		keys[k] = struct{}{}
	}
	for k := range m.right.byKey {
		keys[k] = struct{}{}
	}
	out := make([]OutRow, 0)
	for key := range keys {
		ls := m.left.rows(key)
		rs := m.right.rows(key)
		switch {
		case len(ls) > 0 && len(rs) > 0:
			for _, l := range ls {
				for _, r := range rs {
					out = append(out, paired(key, l, r))
				}
			}
		case len(ls) > 0:
			for _, l := range ls {
				out = append(out, leftPad(key, l))
			}
		case len(rs) > 0:
			for _, r := range rs {
				out = append(out, rightPad(key, r))
			}
		}
	}
	sortOutRows(out)
	return out
}

// replay 从空状态按序应用日志条目，物化出视图。
func replay(log []Entry) ([]OutRow, error) {
	mat := map[OutRow]struct{}{}
	for i, e := range log {
		switch e.Kind {
		case '+':
			if _, exists := mat[e.Row]; exists {
				return nil, fmt.Errorf("entry #%d adds an existing row %v", i, e.Row)
			}
			mat[e.Row] = struct{}{}
		case '-':
			if _, exists := mat[e.Row]; !exists {
				return nil, fmt.Errorf("entry #%d retracts a missing row %v", i, e.Row)
			}
			delete(mat, e.Row)
		default:
			return nil, fmt.Errorf("entry #%d has invalid kind %q", i, e.Kind)
		}
	}
	out := make([]OutRow, 0, len(mat))
	for o := range mat {
		out = append(out, cloneOutRow(o))
	}
	sortOutRows(out)
	return out, nil
}

func diffViews(name string, want, got []OutRow) error {
	if len(want) != len(got) {
		return fmt.Errorf("fulljoin: %s mismatch: want %d rows, got %d", name, len(want), len(got))
	}
	for i := range want {
		if !outRowEqual(want[i], got[i]) {
			return fmt.Errorf("fulljoin: %s mismatch at row %d: want %v, got %v", name, i, want[i], got[i])
		}
	}
	return nil
}

func outRowEqual(a, b OutRow) bool {
	if a.Key != b.Key {
		return false
	}
	if !rowPtrEqual(a.Left, b.Left) || !rowPtrEqual(a.Right, b.Right) {
		return false
	}
	return true
}

func rowPtrEqual(a, b *Row) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return *a == *b
}
