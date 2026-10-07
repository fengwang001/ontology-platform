// Package ontology 提供对象类型字段演进的兼容性校验机制。
//
// 核心问题：一次字段定义变更能否在不迁移既有实例数据的前提下，被新版本
// 的读写路径安全接受。判定遵循以下规则：
//
//   - Classify 把每次单字段变更归入唯一一个类别：新增带默认值、
//     新增不带默认值、收紧约束、放宽约束、改变取值类型、删除字段
//     （另有 mixed-constraint 与 no-effective-change 两个哨兵类别）。
//   - Checker.CheckBatch 逐项独立判定后汇总；任一项不兼容则整批拒绝，
//     同一变更项可同时携带多类不兼容原因（位掩码 IncompatKind）。
//   - 收紧类变更要求全部存活实例既有取值满足新约束；放宽类变更恒兼容，
//     不扫描实例；类型变更要求旧取值能被新类型无损重解释
//     （DataType.CanReinterpret 由类型自身精确声明）且满足新约束。
//   - 新增不带默认值的字段仅在 AllowMissing 或存在覆盖全部存活实例的
//     BackfillRule 时兼容。
//   - 链接类型与动作引用通过 Reference 表达对字段语义的依赖；同一判定
//     逻辑在旧/新语义下对任一存活实例结果不同即语义漂移，整体不兼容。
//   - ObjectType.Submit / SubmitCAS 与 WriteInstance 在同一把互斥锁下
//     串行化，等价于某个全序；拒绝的提交零状态变化。
//   - InstanceStore 只保留存活实例，扫描量以 LiveCount() 为上界，
//     ScannedSinceReset 可供测试验证判定开销与历史实例总量无关。
//   - 每次判定通过 AuditSink 记录变更内容、检查依据与结论。
package ontology
