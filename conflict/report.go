package conflict

import "ontology/doc"

// Item 是报告中的一条冲突及其取证（按冲突数逐条取证）。
type Item struct {
	Conflict
	LeftRecord  doc.Record
	RightRecord doc.Record
	HasLeft     bool
	HasRight    bool
}

// Report 是冲突报告。
type Report struct {
	Items []Item
}

// BuildReport 仅依据冲突清单从两个集合取证：每条冲突最多访问一次集合中的记录，
// 不二次遍历全部记录。accesses 记录 map 查找次数，应等于涉及记录的冲突条数。
func BuildReport(cs List, left, right doc.Set) *Report {
	r := &Report{Items: make([]Item, 0, len(cs))}
	for _, c := range cs {
		it := Item{Conflict: c}
		// 每条冲突恰好取一次记录作为证据（一次 map 查找），不遍历集合。
		// 优先取非删除一侧；左删时取右侧。
		if c.LeftAction == ActionDelete {
			if rec, ok := right[c.Key]; ok {
				it.RightRecord, it.HasRight = rec, true
			}
		} else if rec, ok := left[c.Key]; ok {
			it.LeftRecord, it.HasLeft = rec, true
		} else if rec, ok := right[c.Key]; ok {
			it.RightRecord, it.HasRight = rec, true
		}
		r.Items = append(r.Items, it)
	}
	return r
}

// Len 返回冲突条数。
func (r *Report) Len() int { return len(r.Items) }
