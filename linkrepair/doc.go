// Package linkrepair 对损坏快照中的链接实例记录执行修复裁决，
// 保证恢复结果始终满足链接类型声明的基数约束，并对无法调和的冲突
// 给出四类互斥、固定优先级、可区分报告的判定。
//
// 组件按职责拆分为三个相互协作的部分，入口为 Repair：
//
//   - Validator：单条记录的结构有效性判定；
//   - Referencer：完整记录两端对象的引用可用性核对；
//   - Arbiter：去重与基数约束层面的冲突裁决。
//
// 舍弃原因固定优先级（见 repair.go 中的顺序理由）：
//
//  1. malformed_structure
//  2. reference_unavailable
//  3. cardinality_conflict
//  4. duplicate_record
//
// Repair 是纯函数：不修改输入，可对同一份快照并发、反复调用而不漂移。
package linkrepair
