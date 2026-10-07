// Package aggview 实现跨对象类型的聚合视图子系统：
// 视图声明一种“分组对象类型”与一种或多种“被聚合对象类型”，被聚合实例
// 经由显式声明的链接类型归属到分组实例，引擎对同组实例的某个数值属性求和。
//
// 核心保证：

//   - 增量维护：属性写入只触及该实例当前归属分组；链接增删只触及
//     原分组与新分组（常数份聚合更新），从不扫描无关分组。
//   - 原子处理单元：属性写入与聚合更新、原分组扣减与新分组增加、
//     删除级联均在同一处理单元内完成；任一步失败整体回滚（undo 日志）。
//   - 可串行化并发：所有写操作经由单一互斥锁串行化；归属改变额外用
//     每个成员单调递增的归属版本做乐观并发控制，版本不符以 KindConflict
//     拒绝且无副作用。
//   - “不存在”与零区分：Value{Present:false} 贡献为零且不计入实例计数。
//   - 多分组归属策略必须在视图声明中显式给出（PolicyFull / PolicyEvenShare），
//     不允许静默只算入一个分组。
//   - 精确算术：求和使用 math/big.Rat，EvenShare 份额调整无浮点误差。
//
// 典型用法：
//
//	st := aggview.NewStore()
//	eng := aggview.NewEngine(st, logger)
//	_ = eng.RegisterView(aggview.ViewDef{
//	    Name:      "payroll",
//	    GroupType: "Dept",
//	    Sources: []aggview.Source{{
//	        ObjectType: "Emp", LinkType: "belong", PropType: "salary",
//	        Policy:     aggview.PolicyFull,
//	    }},
//	})
//	// ... CreateObject / AddLink / SetProperty ...
//	a := eng.Query("payroll", "dept-1") // *big.Rat 和 + 实例计数
package aggview
