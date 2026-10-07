// Package snapshot 实现本体平台分块快照的导出、只读完整性校验与
// 跨类型聚合。
//
// 快照按对象类型拆分为多个 JSON 块文件（见 Export），每块自带块头声明
// 条数与 sha256 记录校验和。Loader 对一份已落盘导出做无状态只读校验，
// 区分并报告五类互斥问题：
//
//   - KindOutOfRange：请求类型不在本次导出覆盖范围；
//   - KindIntegrityFailure：块级完整性校验失败（结论只针对本块）；
//   - KindCountMismatch：校验通过但声明条数 != 实际条数，整块记录不可信；
//   - KindDanglingRef：目标类型全部块可信但目标对象不存在；
//   - KindRefUnverifiable：目标类型缺失或目标块不可信，悬空与否不可判定。
//
// 拒绝优先级为 out_of_range > integrity_failure > count_mismatch >
// dangling_reference / reference_unverifiable；一次请求命中的全部问题都会
// 报告并携带具体块与原因。聚合视图仅在全部参与块可信且相互引用全部可解析
// 时生成；聚合失败不影响单独取出已通过校验的块。
//
// Loader 不持有可变状态，并发只读校验结论确定、可重复且幂等。引用查找为
// 期望 O(1)（开放寻址哈希，见 countingIndex），不随目标块记录数线性增长。
//
// 设计取舍、被放弃方案与本地验证方法见 snapshot/DESIGN.md。
package snapshot
