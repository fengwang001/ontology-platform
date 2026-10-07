// Package audit 实现本体平台的动作事务审计溯源子系统。
//
// 三类协作模块：
//
//   - Executor / StateStore：执行动作事务与订正，保证动作的状态
//     变更与审计记录在同一临界区内同生同灭；
//   - AuditLog：按对象类型组织的只增审计序列，分配类型内严格
//     递增、无空洞的序号，并以 SHA-256 哈希链防篡改；
//   - Replayer / NaiveModel：周期快照的区间重放器，以及每次从
//     头线性扫描的朴素参考模型（用于差分测试）。
//
// 记录类型：KindAction（提交）、KindRollback（回退，Before==After，
// 重放跳过）、KindCorrection（订正，指向某条已提交动作且不能指向
// 另一条订正）。错误区分为 IllegalRequestError（参数非法，优先级
// 最高）与 AuditWriteError（审计写入失败导致整体回退）。
//
// 详见 docs/design.md。
package audit
