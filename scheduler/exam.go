package scheduler

import "sort"

// exam.go 负责考试静态信息、时长与加时计算。

// examInfo 保存一门考试的归一化静态信息（去重、排序后的学生集合）。
type examInfo struct {
	exam             Exam
	students         []string // 升序、去重
	extendedStudents map[string]struct{}
	extendedCount    int
}

func newExamInfo(ex Exam) examInfo {
	set := map[string]struct{}{}
	for _, s := range ex.Students {
		set[s] = struct{}{}
	}
	ext := map[string]struct{}{}
	if ex.AllowExtension {
		for _, s := range ex.ExtendedStudents {
			if _, ok := set[s]; ok {
				ext[s] = struct{}{}
			}
		}
	}
	students := make([]string, 0, len(set))
	for s := range set {
		students = append(students, s)
	}
	sort.Strings(students)
	return examInfo{
		exam:             ex,
		students:         students,
		extendedStudents: ext,
		extendedCount:    len(ext),
	}
}

// actualSlots 返回一门考试实际占用的整时段数：
//
//	不允许加时，或没有被登记为需要延长时间的学生：标准时段数；
//	否则：标准时段数 + ceil(标准时段数 * 加时比例)。
//
// 比例换算不足一个时段的部分向上进位到整时段；
// 实际占用取所有参考学生中最长者（延长学生占用的区间即全体区间）。
func actualSlots(info examInfo, ratio float64) int {
	base := info.exam.StandardSlots
	if !info.exam.AllowExtension || info.extendedCount == 0 {
		return base
	}
	return base + ceilSlots(base, ratio)
}

// slotsBetween 返回左闭右闭的整时段集合 [start, end]。
func slotsBetween(start, end int) []int {
	if end < start {
		return nil
	}
	out := make([]int, 0, end-start+1)
	for s := start; s <= end; s++ {
		out = append(out, s)
	}
	return out
}
