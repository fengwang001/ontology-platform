package cdc

// splitChange 将单条变更拆分为事件序列（不含序号分配）。
// Insert -> [WRITE]；Delete -> [DELETE]；
// Update 主键不变 -> [WRITE]；主键变化 -> [DELETE old, WRITE new]。
func splitChange(c Change) []Event {
	switch c.Type {
	case ChangeInsert:
		return []Event{{Type: EventWrite, Key: c.After.Key, Row: c.After}}
	case ChangeDelete:
		return []Event{{Type: EventDelete, Key: c.Before.Key}}
	case ChangeUpdate:
		if c.Before.Key == c.After.Key {
			return []Event{{Type: EventWrite, Key: c.After.Key, Row: c.After}}
		}
		return []Event{
			{Type: EventDelete, Key: c.Before.Key},
			{Type: EventWrite, Key: c.After.Key, Row: c.After},
		}
	}
	return nil
}

// mergeEvents 合并拆分序列：同一个键只保留它在序列中的最后一条事件，
// 输出顺序以各键最后一次出现的位置为准。
func mergeEvents(events []Event) []Event {
	last := make(map[string]int, len(events))
	for i, e := range events {
		last[e.Key] = i
	}
	merged := make([]Event, 0, len(last))
	for i, e := range events {
		if last[e.Key] == i {
			merged = append(merged, e)
		}
	}
	return merged
}
