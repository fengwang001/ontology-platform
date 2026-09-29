package ontology

// mergeBatch 对一批事件做校验与批内合并，返回按“键首次出现顺序”排列的输出。
// rows 为批前表（只读），columns 为合法列集合。
// 任何一条事件非法都会返回 *BatchError，调用方必须整批拒绝。
func mergeBatch(columns []string, rows map[string]map[string]ColumnValue, events []Event) ([]Outcome, *BatchError) {
	_ = columns
	_ = rows
	_ = events
	return nil, nil
}
