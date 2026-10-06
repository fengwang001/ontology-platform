package scheduler

import "sort"

// conflict.go 负责学生冲突的检测（重叠 / 超门数 / 间隔不足）。
//
// 三条学生约束（对同一学生）：
//  1. 任意两门考试占用时段集合不得有交集（重叠）；
//  2. 同一日被安排的考试门数不得超过上限（超门数）；
//  3. 同一日相邻两门考试之间至少间隔 minGap 个空闲时段，
//     即后一门 start - 前一门 end - 1 >= minGap；恰好等于下限视为满足。
//
// 多条同时成立时按「重叠 > 超门数 > 间隔不足」只报一条；
// 同类中报学生标识（字典序）最小者。

// studentConflict 报告一个学生冲突的归因。
type studentConflict struct {
	student  string
	category ConflictCategory
	exam     string
}

// interval 是一门已安排考试占用的左闭右闭区间。
type interval struct {
	examID string
	start  int
	end    int
}

// detectStudent 对单个学生的全部已安排区间（含候选考试）做判定。
// 调用方保证 iv 只包含该学生参加的考试，因此本函数开销为 O(k log k)，
// k 为该学生参加的考试总数，与全局考试数、考场数、时段数无关。
// 类别选择遵循 重叠 > 超门数 > 间隔不足。
func detectStudent(iv []interval, cal *Calendar, maxPerDay, minGap int) ConflictCategory {
	sort.Slice(iv, func(i, j int) bool {
		if iv[i].start != iv[j].start {
			return iv[i].start < iv[j].start
		}
		return iv[i].end < iv[j].end
	})

	// 1. 重叠：排序后只需检查相邻区间。
	for i := 1; i < len(iv); i++ {
		if iv[i].start <= iv[i-1].end {
			return ConflictOverlap
		}
	}

	// 2. 超门数：按日统计。
	perDay := map[int]int{}
	for _, x := range iv {
		perDay[cal.dayOf(x.start)]++
	}
	for _, n := range perDay {
		if n > maxPerDay {
			return ConflictOverCount
		}
	}

	// 3. 间隔不足：仅检查同一日相邻区间（区间跨日已在安排前拒绝）。
	for i := 1; i < len(iv); i++ {
		if cal.sameDay(iv[i-1].start, iv[i].start) {
			gap := iv[i].start - iv[i-1].end - 1
			if gap < minGap {
				return ConflictGapTooSmall
			}
		}
	}
	return ""
}

// detectAll 对 placements 涉及的全部学生做一次全量扫描，
// 返回按优先级归一并取最小学生标识后的首个冲突；无冲突返回 nil。
// 用于朴素对照与全量校验；在线安排使用 studentIndex 做单学生判定。
func detectAll(
	placements map[string]placed,
	infos map[string]examInfo,
	cal *Calendar,
	maxPerDay, minGap int,
) *studentConflict {
	byStudent := map[string][]interval{}
	for eid, p := range placements {
		iv := interval{examID: eid, start: p.start, end: p.extEnd}
		for _, s := range infos[eid].students {
			byStudent[s] = append(byStudent[s], iv)
		}
	}

	students := make([]string, 0, len(byStudent))
	for s := range byStudent {
		students = append(students, s)
	}
	sort.Strings(students)

	// 按类别优先级逐轮扫描，保证同类取最小学生标识。
	for _, cat := range []ConflictCategory{ConflictOverlap, ConflictOverCount, ConflictGapTooSmall} {
		for _, s := range students {
			if detectStudent(byStudent[s], cal, maxPerDay, minGap) == cat {
				return &studentConflict{student: s, category: cat}
			}
		}
	}
	return nil
}

// studentIndex 为在线判定维护「学生 -> 其已安排区间（按时段升序）」。
// 增删一门考试的代价为 O(k)（k 为该考试涉及学生各自的考试数之和），
// 判定一个候选学生只遍历该学生自己的区间，不触碰其他任何规模。
type studentIndex struct {
	per map[string][]interval
}

func newStudentIndex() *studentIndex {
	return &studentIndex{per: map[string][]interval{}}
}

func (idx *studentIndex) add(students []string, iv interval) {
	for _, s := range students {
		list := idx.per[s]
		pos := sort.Search(len(list), func(i int) bool { return list[i].start >= iv.start })
		list = append(list, interval{})
		copy(list[pos+1:], list[pos:])
		list[pos] = iv
		idx.per[s] = list
	}
}

func (idx *studentIndex) remove(students []string, start int) {
	for _, s := range students {
		list := idx.per[s]
		pos := sort.Search(len(list), func(i int) bool { return list[i].start >= start })
		if pos < len(list) && list[pos].start == start {
			list = append(list[:pos], list[pos+1:]...)
		}
		idx.per[s] = list
	}
}

// check 判定单个学生在加入候选区间 cand 后是否冲突，
// 返回冲突类别（无冲突为 ""）。复杂度 O(k log k)，k 为该学生考试数。
func (idx *studentIndex) check(s string, cand interval, cal *Calendar, maxPerDay, minGap int) ConflictCategory {
	list := idx.per[s]
	merged := make([]interval, 0, len(list)+1)
	merged = append(merged, list...)
	merged = append(merged, cand)
	return detectStudent(merged, cal, maxPerDay, minGap)
}
