// Package snapshot 实现本体子图导出快照的完整性校验与损坏定位。
//
// 快照是有序的记录序列，每条记录为对象记录（KindObject）或链接记录
// （KindLink）。链接记录引用的两个对象标识必须在此前已出现且自身合法，
// 否则构成引用缺失。
//
// 校验按记录顺序单次扫描，命中第一条损坏记录即停止：
//
//   - 结果 Status 为 StatusComplete（整序列自洽）或 StatusTruncated；
//   - StatusTruncated 时 PrefixLen 为最大可恢复前缀长度，BadIndex 为损坏
//     记录下标，Reason 为三类原因之一；
//   - 空序列返回 StatusComplete；第一条即损坏时 PrefixLen 为 0 且仍给出
//     Reason，二者语义不混用。
//
// 原因判定次序（仅针对实际导致截断的那一条记录）：
//
//  1. ReasonObjectCorrupt —— 对象记录自身字段损坏（含重复 ID 类型冲突、
//     以及无法证明为链接记录的未知 Kind）；
//  2. ReasonLinkCorrupt —— 链接记录自身字段损坏；
//  3. ReasonLinkReferenceMissing —— 链接引用前方缺失。
//
// 同标识的对象记录重复出现：类型相同视为冗余，类型不同则第二次出现自身
// 损坏。校验不修改输入、不持有包级可变状态；通过 Reader 消费，命中即停，
// 实际读取记录数不超过“最大可恢复前缀长度 + 1”。
package snapshot
