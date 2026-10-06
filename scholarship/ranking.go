package scholarship

import "sort"

// lessStudent 排序键: 平均成绩降序 -> 本周期学分降序 -> 荣誉积分降序。
// 学号比较仅用于让迭代顺序确定, 不影响并列判定。
func lessStudent(a, b *Student) bool {
	if a.AvgGrade != b.AvgGrade {
		return a.AvgGrade > b.AvgGrade
	}
	if a.Credits != b.Credits {
		return a.Credits > b.Credits
	}
	if a.HonorPoints != b.HonorPoints {
		return a.HonorPoints > b.HonorPoints
	}
	return a.ID < b.ID
}

// sameKeys 三级排序键全部相同才视为并列。
func sameKeys(a, b *Student) bool {
	return a.AvgGrade == b.AvgGrade &&
		a.Credits == b.Credits &&
		a.HonorPoints == b.HonorPoints
}

func sortStudents(ss []*Student) {
	sort.SliceStable(ss, func(i, j int) bool { return lessStudent(ss[i], ss[j]) })
}

// tieGroups 将已排序的学生切片切分为并列组, 组内保持排序后的相对顺序。
func tieGroups(sorted []*Student) [][]*Student {
	var groups [][]*Student
	for i, s := range sorted {
		if i == 0 || !sameKeys(sorted[i-1], s) {
			groups = append(groups, []*Student{s})
		} else {
			groups[len(groups)-1] = append(groups[len(groups)-1], s)
		}
	}
	return groups
}

// rankEntries 计算名次: 并列者占据相同名次, 其后名次按人数跳号(1,1,3,...)。
func rankEntries(sorted []*Student) []RankEntry {
	entries := make([]RankEntry, 0, len(sorted))
	rank := 0
	for i, s := range sorted {
		if i == 0 || !sameKeys(sorted[i-1], s) {
			rank = i + 1
		}
		entries = append(entries, RankEntry{StudentID: s.ID, Rank: rank})
	}
	return entries
}
