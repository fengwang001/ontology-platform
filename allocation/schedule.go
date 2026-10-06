package allocation

const maxWeek = 63 // 周次编号 1..63，位图 bit i 表示第 i 周（bit0 保留）

// schedule 教师时段占用表：occ[节次] 为 1..MaxWeek 周次位图。
// 冲突判定开销只与任务占用的节次数有关，与历史任务总数无关（见 docs/DESIGN.md）。
type schedule struct {
	bits   map[int]uint64 // 节次 -> 占用周次位图
	counts map[int]map[int]int
}

func newSchedule() *schedule {
	return &schedule{
		bits:   make(map[int]uint64),
		counts: make(map[int]map[int]int),
	}
}

func weekMask(start, end int) uint64 {
	var m uint64
	for w := start; w <= end; w++ {
		m |= uint64(1) << uint(w)
	}
	return m
}

// overlaps 判断 [start,end] 闭区间周次与给定节次集合是否与已有占用相交。
func (s *schedule) overlaps(start, end int, periods []int) bool {
	m := weekMask(start, end)
	for _, p := range periods {
		if s.bits[p]&m != 0 {
			return true
		}
	}
	return false
}

// add / remove 引用计数式占用，换人释放旧份额后位图立即恢复。
func (s *schedule) add(start, end int, periods []int) {
	for _, p := range periods {
		cm, ok := s.counts[p]
		if !ok {
			cm = make(map[int]int)
			s.counts[p] = cm
		}
		for w := start; w <= end; w++ {
			if cm[w] == 0 {
				s.bits[p] |= uint64(1) << uint(w)
			}
			cm[w]++
		}
	}
}

func (s *schedule) remove(start, end int, periods []int) {
	for _, p := range periods {
		cm := s.counts[p]
		for w := start; w <= end; w++ {
			cm[w]--
			if cm[w] <= 0 {
				delete(cm, w)
				s.bits[p] &^= uint64(1) << uint(w)
			}
		}
	}
}
