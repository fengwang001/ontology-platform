// Package loto 实现生产现场能量隔离上锁挂牌（Lockout/Tagout）工作票系统。
//
// 它管理检修工作票的申请、审批、多人多锁隔离、零能量验证、开工、进出登记、
// 完工、试运行与解锁，并提供只读送电判定，保证：
//
//	任何设备在有人可能作业时都不可能被送电。
//
// 快速上手：
//
//	s := loto.New()
//	_ = s.RegisterPerson("ap", loto.RoleApplicant, loto.RoleWorker)
//	_ = s.RegisterPerson("a1", loto.RoleApprover)
//	_ = s.RegisterPerson("v") // 无角色者可充当外部验证人
//	_ = s.RegisterDevice("D1", []string{"P1", "P2"})
//
//	_ = s.Apply("T1", "ap", []string{"D1"}, loto.WorkNormal, 10, 100, []string{"ap"})
//	_ = s.Approve("T1", "a1", 10)           // 普通票一人批准；高风险票需两个不同批准人
//	_ = s.PlaceLock("T1", "ap", "P1", 11)   // 每位作业人员对每个点各挂一把自己的锁
//	_ = s.PlaceLock("T1", "ap", "P2", 11)
//	_ = s.Verify("T1", "v", 12)             // 全员全部上锁后，由非作业人员验证
//	_ = s.StartWork("T1", "ap", 13)         // 开工时刻须在 [start,end) 内
//	_ = s.Enter("T1", "ap", 14)
//	_ = s.Leave("T1", "ap", 20)
//	_ = s.Complete("T1", "ap", 21)          // 全员离场后完工
//	_ = s.RemoveLock("T1", "ap", "P1", 22)  // 完工后本人摘锁；最后一把摘除才解隔离
//	_ = s.RemoveLock("T1", "ap", "P2", 23)
//	ok, report, _ := s.CanEnergize("D1", 24) // 只读；report.Reasons 给出判定依据

// 错误以 *OpError 返回，其 Code 严格按以下次序（小者优先）：
//
//	InvalidParam > ClockRollback > NotFound > PermissionDenied >
//	StateNotAllowed > Conflict > ConditionNotMet
//
// 所有方法可并发调用（内部串行化，等价于某个合法串行顺序）。
// 详细设计、取舍与验证方法见 DESIGN.md。
package loto
