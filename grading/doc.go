// Package grading 实现阅卷任务分配与双评仲裁引擎。
//
// 核心流程：
//
//	eng := grading.NewEngine(grading.Config{Threshold: 10, Step: 2})
//	eng.AddGrader(grading.Grader{ID: "a", Group: "A", DailyQuota: 80})
//	eng.AddPaper(grading.Paper{ID: "p1", Question: "q", MaxScore: 100, Student: "s1"})
//	for _, t := range eng.Tasks("p1") {
//	    eng.Submit(t.ID, 60)
//	}
//	if fv, ok := eng.FinalScore("p1"); ok { ... }
//
// 约束：两名初评人异组；回避本人名下学生；同一评卷人对同一学生在任务
// 有效期内只出现一次；不得超过当日（在手任务）配额。选人规则为合格者中
// 在手任务最少、并列时标识最小，结果与调用历史无关。
//
// 终分：分差不大于阈值（恰等视为一致）取两初评平均；超过则由第三组仲裁人
// 评分，按“差较小初评、等距取较高初评”与仲裁分平均；两差均超阈值时终分
// 即仲裁分。平均值落在半网格时向满分方向靠拢。
//
// 错误用 *Error 的 Kind 字段分类，固定优先级：
// 参数非法 > 不存在 > 已停用 > 任务状态 > 分数非法 > 无合格候选。
package grading
