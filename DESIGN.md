# 差分隐私预算账本设计说明

## 结构与取舍
1. 三包职责：`plan` 只负责纯函数式的计划求值（开销、分区集、结构校验、`nodes` 计数）；
   `budget` 是并发安全的账目存储（数据集 used/resv、分析师按窗口占用、时钟与 touched 计数）；
   `ledger` 编排预留/启动/结算/撤销的状态机与拒绝次序，本身不另存账目。
2. 账目模型：数据集只记 `used`（只增）与 `resv`（在途），终身额度不恢复；分析师按
   `floor(now/Wn)` 得到的窗口号建稀疏 map，占用只记预留时所属窗口——这样跨窗口后旧查询
   不占新窗口，结算差额自然退回旧窗口，无需迁移或扫描历史窗口，满足 touched 界。
3. 并发：`budget` 一把 RWMutex；`ledger` 一把 Mutex 包住校验+记账+状态迁移整个临界区，
   账目修改经 `budget` 的内部（持锁）方法完成，保证被拒操作零副作用且结果等价某串行序。
4. 撤销语义：Reserved 全额退还（resv/占用各减 cost）；Running 一律按全额转 used、分析师
   占用保持 cost——带噪结果可能已泄露，放弃“按 actual 退还”方案（Commit 前没有 actual）。
5. Sample 取整：自底向上求值，每个 Sample 节点各做一次
   `ceil(child*num/den) = (child*num+den-1)/den`；故三个 Sample(1,3,Leaf(1)) 的 Seq 为 3，
   包裹同一 Seq 的单个 Sample 为 1。Seq 求和、Par 先校验分区两两不交再取最大值。
6. 计划校验与求值在同一趟递归完成（深度 ≤8、节点 ≤256、子数 1..16、c/cost ≤1e12 等），
   每个节点恰好访问一次，非导出计数器 `nodes` 即节点总数；深度按边数计（根 Leaf 深度 1）。
7. 拒绝次序严格按规格实现：Reserve 为 参数非法(含计划结构) > 时钟回退 > qid 重复 >
   实体不存在 > ErrNotDisjoint > ErrDatasetExhausted（名字节序首个不足者）>
   ErrAnalystExhausted；Start/Commit/Cancel 为 参数非法 > 时钟回退 > qid 不存在 >
   状态不符（含已结束）> ErrOverspend。被拒不更新时钟，不改任何账。
8. `touched`：每次 Reserve/Commit/Cancel 一个独立计数器，只统计实际读写的
   数据集账条数、分析师窗口账条数与查询记录条数（去重，每条只算一次）。Reserve 为
   n 数据集 +1 窗口 +1 查询 ≤ n+2；Commit/Cancel 为 n+2（Cancel Reserved 同样只动预留窗口）。
9. 额度与开销均为 int64 微单位；中间和最大上界 256*1e9 < 9.2e18，无溢出。

## 被放弃的方案
- 分析师只保留单一“当前窗口”计数并在结算时处理跨窗口：无法把差额退回旧窗口、无法精确复现，
  改为稀疏 window→占用，且每条查询记住预留窗口，结算/撤销只改该窗口。
- Running 撤销部分或全额退还：违反“噪声结果可能已泄露”，全额计入 used。
- 计划先校验结构再二次求值算开销：会使 nodes 翻倍，改为单趟递归（结构错优先于 NotDisjoint）。
- Reserve 时按数据集列表顺序判不足：规格要求字节序，先排序再检查，首个不足即拒绝、零扣减。

## 本地验证
- `go build ./...`；`go vet ./...`；`gofmt -l .` 无输出。
- `go test -race ./...`：表驱动用例（Seq/Par/Sample、相交、恰等、拒绝次序、状态机、跨窗口、
  Running 撤销全额、nodes 与 touched 界及 100/10000 在途对照）与 1500 组随机序列对照
  朴素重算模拟（每条操作打印输入/输出/判定依据）。
