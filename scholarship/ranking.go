package scholarship

import "sort"

// sortKey 为多级排序键：平均成绩、学分、荣誉，全部降序；
// 末位以学号保证全局确定的遍历顺序（不参与并列判定）。
type sortKey struct {
	average float64
	credits float64
	honor   int
	id      string
}

// rankStudents 按三级排序键排序并赋名次：键全同视为并列、同名次、其后跳号。
// ids 已包含具备该等级资格的学生。
func rankStudents(students map[string]*Student, ids []string) []RankedStudent {
	out := make([]RankedStudent, 0, len(ids))
	for _, id := range ids {
		s := students[id]
		out = append(out, RankedStudent{
			StudentID: s.ID,
			Dept:      s.Dept,
			Average:   s.Average,
			Credits:   s.Credits,
			Honor:     s.Honor,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Average != b.Average {
			return a.Average > b.Average
		}
		if a.Credits != b.Credits {
			return a.Credits > b.Credits
		}
		if a.Honor != b.Honor {
			return a.Honor > b.Honor
		}
		return a.StudentID < b.StudentID // 并列组内确定顺序，不影响名次与整组规则
	})
	// 赋名次：三键全同占据相同名次，其后按人数跳号。
	for i := range out {
		switch {
		case i == 0:
			out[i].Rank = 1
		case tied(out[i-1], out[i]):
			out[i].Rank = out[i-1].Rank
		default:
			out[i].Rank = i + 1
		}
	}
	return out
}

func tied(a, b RankedStudent) bool {
	return a.Average == b.Average && a.Credits == b.Credits && a.Honor == b.Honor
}

// tieGroup 返回从 start 开始、与 out[start] 并列的整组（含末下标+1）。
func tieGroup(out []RankedStudent, start int) int {
	end := start + 1
	for end < len(out) && tied(out[start], out[end]) {
		end++
	}
	return end
}
