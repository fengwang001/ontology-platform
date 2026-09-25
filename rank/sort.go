package rank

// countedSorter 对一个分区内的条目做 O(n log n) 排序并计数比较次数。
// 排序键：先按 Value（方向由 dir 决定），Value 相等时按 ID 升序。
// +0.0 与 -0.0 被视为相等；NaN 必须在排序前由调用方剔除。
type countedSorter struct {
	entries []entry
	dir     Direction
	count   int64
}

func (s *countedSorter) Len() int { return len(s.entries) }

func (s *countedSorter) lessValue(a, b float64) int {
	s.count++
	if a == b || (a == 0 && b == 0) {
		return 0
	}
	if s.dir == Asc {
		if a < b {
			return -1
		}
		return 1
	}
	if a > b {
		return -1
	}
	return 1
}

func (s *countedSorter) lessID(a, b string) bool {
	s.count++
	return a < b
}

func (s *countedSorter) Less(i, j int) bool {
	a, b := s.entries[i], s.entries[j]
	if c := s.lessValue(a.value, b.value); c != 0 {
		return c < 0
	}
	return s.lessID(a.id, b.id)
}

func (s *countedSorter) Swap(i, j int) {
	s.entries[i], s.entries[j] = s.entries[j], s.entries[i]
}

// sameValue 判断排序值是否构成并列（NaN 已在排序前剔除）。
func sameValue(a, b float64) bool {
	return a == b || (a == 0 && b == 0)
}
