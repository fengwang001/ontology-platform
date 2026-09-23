package doc

// Record 是一条记录：字段名 → 值。
type Record map[string]Value

// Set 是记录集合：键 → 记录。
type Set map[string]Record

// CloneSet 返回集合的深拷贝。
func CloneSet(s Set) Set {
	out := make(Set, len(s))
	for k, r := range s {
		nr := make(Record, len(r))
		for f, v := range r {
			nr[f] = v
		}
		out[k] = nr
	}
	return out
}

// SortedKeys 返回字典序排列的键。
func (s Set) SortedKeys() []string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

// SortedFields 返回字典序排列的字段名。
func (r Record) SortedFields() []string {
	fields := make([]string, 0, len(r))
	for f := range r {
		fields = append(fields, f)
	}
	sortStrings(fields)
	return fields
}
