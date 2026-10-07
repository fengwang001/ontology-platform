// Package compensate 实现本体平台“动作副作用补偿回滚”子系统。
//
// 一次动作（ActionSpec）声明若干条副作用分支（BranchSpec）。分支内部的子操作
// （WriteOp）严格按声明顺序生效；分支间可用 Deps 声明“被依赖方完全生效后才能开始”。
// 任一分支最终失败（子操作自身失败或上游失败向下游传播）时，系统按跨分支依赖逆序、
// 分支内子操作逆序，补偿该动作全部已生效副作用。
//
// 典型用法：
//
//	g := compensate.NewGraph()
//	log := compensate.NewTextLogger(os.Stdout)
//	e := compensate.NewEngine(g, log)
//	action, err := e.Declare(spec) // 声明期完成环检测；有环返回 KindDependencyCycle
//	if err != nil {
//		// 声明被拒绝，对象图无任何改动
//	}
//	rep := action.Execute(ctx)                    // 并发生效；失败时自动补偿
//	err = action.RequestDirectCompensation("up") // 外部直接补偿；违反逆序时被拒绝
//
// 设计细节、关键取舍、被放弃方案与本地验证见同目录 DESIGN.md。
package compensate
