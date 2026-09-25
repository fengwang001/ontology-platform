# 带撤回的增量聚合视图维护器 — 设计推导

## 1. 变更模型

`change.Change{Ver, Op, Group, ID, Val}`：`Ver` 单调递增版本号；`Op ∈
{Insert,Delete,Update}`；`Group` 为指向字符串的指针，可区分"空串分组"(合法)
与"缺失分组"(拒绝)；`ID` 标识被更新的记录。删除携带其当前分组与值，更新携带
旧组/旧值（ID 相同即同一条记录，新组/新值随更新给出）。

## 2. 五个聚合器的撤回推导

删除一条值为 v 的记录时，设旧聚合值为 A：

| 聚合 | 插入 | 删除 | 是否需要该组成员 |
|---|---|---|---|
| Count | A+1 | A-1，恒成立 | 否 |
| Sum | A+v | A-v，恒成立 | 否 |
| Min | min(A,v) | 仅当 v≠A 时 A 不变；**v==A 时新极值可能是组内其余任意成员**，状态中没有候选，必须重算 | 条件需要 |
| Max | max(A,v) | 同上：v==A 时必须重算 | 条件需要 |
| DistinctCount | 加入集合 | 必须知道**是否还有另一条记录持有 v**；集合只有去重信息，无法回答计数，故每次删除都需成员重算 | 是 |

Count/Sum 的新值只由 (A,v) 决定，与其他成员无关，故可直接撤回。Min/Max
在删掉非极值时无影响（例外路径），删到极值才回退到成员重算；DistinctCount
的计数依赖每个值的持有数，按题意每次删除走重算。重算是例外：
`recomputeN`（次数）与 `recomputeRows`（访问成员条数）两个非导出计数随
Stats 暴露。±0 在 IEEE754 数值上相等（+0==-0），Min/Max 判定极值用
`v == cur`；NaN 在入口拒绝，不进入聚合。

## 3. 视图编排（Apply → Recompute → Commit）

每个变更包构成一个 Staging：Apply 按顺序应用所有行级变更，维护
`members[group] map[id]value`，先让 Count/Sum/Min/Max 增量改动并记录
"删除到极值"的组；Recompute 只对被标记组与 DistinctCount 受影响组，用该组
当前成员重建聚合（访问数 ≤ 该组成员数，绝不扫描其他组）；Commit 先把本批
编码追加到 journal（fsync 后视为提交），再一次性发布新状态与新 maxVer。
组在成员归零时整体删除，查询返回 `(zero,false)` 而非零值。

## 4. 版本单调与幂等

判定依据：状态只依赖 `Ver ≤ maxVer` 的完整日志前缀。
`Ver < maxVer` → `ErrVersionBackward`（拒绝，计 rejected，视图不变）；
`Ver == maxVer` → 幂等：同一批/同一变更重复提交逐字段不变（重复批直接返回
nil 不写日志）；`Ver > maxVer` 才接受。乱序（低于已见版本）计入
`Stats.Rejected`。缺失分组、NaN 同样拒绝并计数。

## 5. Journal 格式与截断分类

文件头：魔数 `"ONTJ"` + 版本字节 1（5 字节）。每条记录：
`uint32 BE 长度 n`（payload 字节数）+ payload + `uint32 BE CRC32-IEEE`
（对 payload）。回放逐记录解析，末尾残缺分四类（均为可 `errors.Is` 判定的
哨兵错误）：

- `ErrHeaderIncomplete`：文件头不足 5 字节；
- `ErrLenIncomplete`：长度字段剩余不足 4 字节；
- `ErrBodyIncomplete`：宣称 n 字节的 body 读不全；
- `ErrCRC`：body 完整但 CRC 字段不足或不匹配。

Replay 返回已完整记录与首个错误；视图只装载完整前缀，半截记录永不生效。

## 6. 崩溃恢复与并发

journal 是唯一持久真相，Commit 之后崩溃等价于正常提交（重放得到该批）；
Commit 之前（Apply 中 / Recompute 中 / Commit 前）崩溃，该批未写入日志，
重放得到的状态 == 不崩溃地跑到"此前所有已提交批"的状态。用
`crashHook{afterApply,afterRecompute,beforeCommit}` 注入 `runtime.Goexit`
式中断模拟，新建视图重放后与参照视图逐字段相同。

视图用单一 RWMutex 串行化提交：读者持 RLock 取整组快照（或值+存在位），
不可能看到组内 Count/Sum 半更新；Recompute 与写入在同一临界区内完成，
同组并发写入排队应用，不丢失。

## 7. 审计

`audit.Compare` 忽略增量状态，仅用 `view.Records()` 的当前成员全量重算五
个聚合，逐组比对；Sum 用 `math.Float64bits` 位级比较，组集合按
"增量多出/增量缺失"双向校验。
