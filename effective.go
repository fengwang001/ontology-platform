package audit

// 本文件负责从学生原始修读记录构造"可计入项"：
// 及格判定、同课多次修读去重、课程替换、转入学分上限截断。

import (
	"fmt"
	"sort"
)

// rawAttempt 一条未撤销的本校修读（含不及格），用于未结清检查。
type rawAttempt struct {
	course   string
	semester string
	grade    float64
}

// countedItem 一门可能计入的课程项（同一课程身份至多一个）。
type countedItem struct {
	sourceID string // 记录 ID；转入项为 "transfer:<seq>"
	taken    string // 实际修读课程
	identity string // 计入身份（替换后为目标课程）
	credits  float64
	grade    float64
	semester string
	transfer bool
	viaSub   *Substitution // 非空表示经替换计入
}

// studentContext 审核一个学生所需的全部已隔离数据。
// 该结构只包含该学生自己的记录，审核开销与其他学生数量无关。
type studentContext struct {
	student string
	plan    *PlanVersion

	// canonical 池：每个本校课程身份保留最优一次；转入按上限截断。
	items []*countedItem
	// 全部未撤销本校修读尝试（含不及格），按学期稳定排序。
	attempts []rawAttempt
	// 每个课程身份可使用的替换（含"不替换"），按替换 ID 排序保证确定性。
	subOptions map[string][]*Substitution
	// 课程主数据学分。
	courseCredit map[string]float64
	// touches 审核访问的记录条数（用于可验证的复杂度隔离证明）。
	touches int
}

func buildContext(e *Engine, s *Student) *studentContext {
	ctx := &studentContext{
		student:      s.ID,
		plan:         e.planFor(s),
		subOptions:   map[string][]*Substitution{},
		courseCredit: map[string]float64{},
	}
	ctx.collect(e, s)
	return ctx
}

func (ctx *studentContext) collect(e *Engine, s *Student) {
	for id, c := range e.courses {
		ctx.courseCredit[id] = c.Credits
	}

	// 1) 全部未撤销本校修读尝试（用于未结清检查），按学期、记录 ID 稳定排序。
	var attempts []rawAttempt
	byCourse := map[string][]*Record{}
	for _, rid := range e.studentRecs[s.ID] {
		rec := e.records[rid]
		if rec.Revoked {
			continue
		}
		attempts = append(attempts, rawAttempt{rec.Course, rec.Semester, rec.Grade})
		byCourse[rec.Course] = append(byCourse[rec.Course], rec)
	}
	sort.Slice(attempts, func(i, j int) bool {
		if attempts[i].semester != attempts[j].semester {
			return semesterLE(attempts[i].semester, attempts[j].semester)
		}
		return attempts[i].course < attempts[j].course
	})
	ctx.attempts = attempts

	ctx.touches += len(e.studentRecs[s.ID]) + len(e.transfers[s.ID])

	// 2) 同课程多次修读：仅最高成绩一次可计入；成绩相同取最早学期。
	//    仅收集达到及格线的记录；不及格记录不进入计入池。
	for course, recs := range byCourse {
		var best *Record
		for _, rec := range recs {
			if !ge0(rec.Grade, ctx.plan.PassLine) {
				continue
			}
			if best == nil || betterAttempt(rec, best) {
				best = rec
			}
		}
		if best == nil {
			continue
		}
		ctx.items = append(ctx.items, &countedItem{
			sourceID: best.ID,
			taken:    best.Course,
			identity: course,
			credits:  best.Credits,
			grade:    best.Grade,
			semester: best.Semester,
		})
	}

	// 3) 转入学分：按登记次序累计，超过方案上限的靠后记录不计入。
	//    同一课程身份的本校最优记录与转入记录并存时，仍只取成绩最优一次
	//    （成绩相同取学期更早；再相同取来源 ID 更小）。
	var used float64
	for _, tr := range e.transfers[s.ID] {
		if used+tr.Credits > ctx.plan.TransferCap+eps {
			continue
		}
		used += tr.Credits
		ti := &countedItem{
			sourceID: fmt.Sprintf("transfer:%d", tr.Seq),
			taken:    tr.Course,
			identity: tr.Course,
			credits:  tr.Credits,
			grade:    tr.Grade,
			semester: tr.Semester,
			transfer: true,
		}
		replaced := false
		for k, existing := range ctx.items {
			if existing.identity == tr.Course {
				if betterItem(ti, existing) {
					ctx.items[k] = ti
				}
				replaced = true
				break
			}
		}
		if !replaced {
			ctx.items = append(ctx.items, ti)
		}
	}

	// 4) 替换选项：从 (方案, 版本, 源课程) 索引桶直接取，不扫描全局替换表。
	for _, it := range ctx.items {
		if it.transfer {
			continue
		}
		opts := []*Substitution{nil}
		bucket := e.subIndex[s.PlanID][s.PlanVersion][it.taken]
		ctx.touches += len(bucket)
		for _, sub := range bucket {
			if semesterLE(sub.Effective, it.semester) {
				opts = append(opts, sub)
			}
		}
		ctx.subOptions[it.sourceID] = opts
	}

	// 课程项按 sourceID 排序，使枚举顺序与登记顺序无关。
	sort.Slice(ctx.items, func(i, j int) bool { return ctx.items[i].sourceID < ctx.items[j].sourceID })
}

// betterItem 同一课程身份的两条候选记录：成绩高者胜；
// 成绩相同取更早学期；再相同取来源 ID 更小者（本校记录 ID 通常字典序靠前与否不影响规则确定性）。
func betterItem(a, b *countedItem) bool {
	if a.grade != b.grade {
		return a.grade > b.grade
	}
	if a.semester != b.semester {
		return semesterLE(a.semester, b.semester)
	}
	return a.sourceID < b.sourceID
}

// betterAttempt 判断 a 是否优于 b：成绩高者胜；成绩相同取学期更早者。
func betterAttempt(a, b *Record) bool {
	if a.Grade != b.Grade {
		return a.Grade > b.Grade
	}
	if a.Semester != b.Semester {
		return semesterLE(a.Semester, b.Semester)
	}
	return a.ID < b.ID
}

// resolveOption 返回某项在给定替换选择下的（身份、学分）。
// nil 表示不替换；替换后学分取两门课程学分较小者。
func (ctx *studentContext) resolveOption(it *countedItem, sub *Substitution) (string, float64) {
	if sub == nil {
		return it.identity, it.credits
	}
	credit := it.credits
	if toCredit, ok := ctx.courseCredit[sub.To]; ok && toCredit < credit {
		credit = toCredit
	}
	return sub.To, credit
}

// semesterLE a <= b 的学期比较：按 "年-季" 解析，无法解析时退回字典序。
func semesterLE(a, b string) bool {
	var ya, sa, yb, sb int
	if n, _ := sscanfSemester(a, &ya, &sa); n != 2 {
		return a <= b
	}
	if n, _ := sscanfSemester(b, &yb, &sb); n != 2 {
		return a <= b
	}
	if ya != yb {
		return ya < yb
	}
	return sa <= sb
}

// sscanfSemester 解析 "年-季" 形式学期（如 "2023-1"）。
func sscanfSemester(s string, year, term *int) (int, error) {
	var y, t int
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			for _, ch := range s[:i] {
				if ch < '0' || ch > '9' {
					return 0, errBadSemester
				}
			}
			for _, ch := range s[i+1:] {
				if ch < '0' || ch > '9' {
					return 0, errBadSemester
				}
			}
			for _, ch := range s[:i] {
				y = y*10 + int(ch-'0')
			}
			for _, ch := range s[i+1:] {
				t = t*10 + int(ch-'0')
			}
			*year, *term = y, t
			return 2, nil
		}
	}
	return 0, errBadSemester
}

var errBadSemester = &OpError{Code: ErrInvalidArgument, Message: "invalid semester"}
