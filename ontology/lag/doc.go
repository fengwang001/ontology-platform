// Package lag 对“窗口函数取前驱值”（LAG）做增量维护：面对按分区排序的
// 行变更流（插入/删除），维护每行的前驱取值并输出确定顺序的变更日志。
//
// # 排序规则
//
// 不同分区（Row.Partition）完全隔离、互不影响。同一分区内按
// (SortKey 升序, ID 升序) 形成全序：先比较排序键，排序键并列时以行标识
// 的字典序打破并列，因此结果与插入顺序无关、可复现。
//
// # 前驱取值
//
// 一行的前驱值是同分区内紧邻其前一行的 Value；分区首行没有前驱。
// “没有前驱”与“前驱取值为零值字符串 ""”通过 HasPrev 严格区分：
// 首行 HasPrev=false；其余行 HasPrev=true 且 Prev 为前行的 Value。
//
// # 变更日志规则
//
// 每次提交返回一个 Change 切片，顺序固定：
//
//   - 插入：第一条恒为新行的 insert（携带其前驱值）；若紧邻后继的前驱
//     取值发生变化（含 空→非空、非空→非空但值不同），追加该后继的
//     update。前驱值未变的后继不输出任何条目。
//   - 删除：第一条恒为被删行的 delete；若紧邻后继的前驱取值发生变化
//     （含 非空→空），追加该后继的 update；值未变则不输出。
//
// 下游（Materializer）按日志顺序应用 insert/update/delete 后得到的视图，
// 与引擎增量视图以及 Recompute 的批量重算结果逐分区、逐标识一致。
//
// # 边界与错误类别
//
// 非法输入被整体拒绝，原因（Reason）互不相同、可区分：
//
//   - ReasonEmptyPartition：分区名为空；
//   - ReasonEmptyID：行标识为空；
//   - ReasonDuplicateID：插入的标识在该分区已存在；
//   - ReasonMissingID：删除的标识在该分区不存在；
//   - ReasonRowLimitExceeded：插入后行数超过上限（WithMaxRows，
//     默认 DefaultMaxRows）。
//
// 拒绝在任何状态修改之前完成：行、视图、已提交日志均保持调用前状态，
// 失败不留痕。
//
// # 并发
//
// 所有方法可被多个执行体并发调用。Insert/Delete 持写锁，View/Snapshot/
// TotalRows/Verify/AllRows 持读锁，视图与自检可与提交并发，且每次调用
// 都看到一致快照。
package lag
