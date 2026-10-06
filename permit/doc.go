// Package permit 实现行政许可并联审批办理服务。
//
// 一件许可包含多个部门环节，环节间为无环依赖（DAG）。本包处理受理、补正暂停/恢复计时、
// 工作日时限、超时默认通过、一环节不通过后的整件终止与申请人撤回，并支持对任意历史时刻
// 精确、可复现地查询每个环节的状态、剩余时限、超时标记与整件进度。
//
// 核心设计为事件溯源 + 纯函数惰性派生：变更操作仅追加不可变事件，查询时由
// derive(case, day) 重放并派生，保证相同操作序列得到完全相同结果；变更在互斥锁内串行化，
// 并发等价于某个串行顺序；时限判定只依赖本许可数据，开销不随在办许可总数增长。
//
// 快速上手：
//
//	svc := permit.NewService(nonWorkingDays,
//	    permit.WithSupplement(5, 1),                 // 补正期限工作日数、每环节补正上限
//	    permit.WithLogger(myLogger))                // 可选：逐步日志
//	_ = svc.RegisterType(&permit.PermitType{
//	    ID: "build",
//	    Stages: []permit.StageDef{
//	        {ID: "land", Department: "自然资源局", DueWorkdays: 5},
//	        {ID: "fire", Department: "消防救援机构", DueWorkdays: 10,
//	            Prereqs: []string{"land"}, AutoPass: true},
//	    },
//	})
//	_ = svc.Accept(day1, "case-1", "build")
//	_ = svc.Decide(day3, "case-1", "land", "自然资源局", permit.DecideApprove)
//
//	view, _ := svc.StageAt(day8, "case-1", "fire") // 历史时刻查询
//	prog, _ := svc.Progress(day8, "case-1")
//
// 错误通过 *Error 暴露可区分的 ErrorCode，按规定优先级返回。
// 详见 docs/permit-design.md。
package permit
