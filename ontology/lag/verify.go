package lag

import "fmt"

// Verify 用与引擎无关的批量重算逻辑重算全部分区，并与增量维护的当前视图
// 逐分区、逐标识比较。一致时返回 nil；不一致时返回描述首个差异的错误。
//
// 该方法可被多个执行体并发调用，也可与提交并发。
func (e *Engine) Verify() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	batch := recompute(e.parts)
	if len(batch) != len(e.parts) {
		return fmt.Errorf("lag: verify partition count mismatch: incremental=%d batch=%d",
			len(e.parts), len(batch))
	}
	for part, inc := range batch {
		got, ok := e.parts[part]
		if !ok {
			return fmt.Errorf("lag: verify missing incremental partition %q", part)
		}
		live := orderedView(got)
		if len(live) != len(inc) {
			return fmt.Errorf("lag: verify partition %q row count mismatch: incremental=%d batch=%d",
				part, len(live), len(inc))
		}
		for i := range inc {
			a, b := live[i], inc[i]
			if a.ID != b.ID || a.SortKey != b.SortKey || a.Value != b.Value ||
				a.HasPrev != b.HasPrev || a.Prev != b.Prev {
				return fmt.Errorf("lag: verify partition %q position %d mismatch: incremental=%+v batch=%+v",
					part, i, a, b)
			}
		}
	}
	return nil
}

// recompute 从零对给定行做批量重算，不依赖任何增量状态。
func recompute(parts map[string]map[string]*entry) map[string][]RowView {
	out := make(map[string][]RowView, len(parts))
	for part, rows := range parts {
		out[part] = orderedView(rows)
	}
	return out
}
