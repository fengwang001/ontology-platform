# FINDINGS: 重叠写劈开后 Corrections 轨迹的污染问题

测试依据：`corrections_split_test.go`（characterization tests，全部断言当前真实行为并通过）。
以下每条给出复现输入、实际输出、应当输出与根因分析。

## 1. 残差区域出现"幻影修正"：相邻条目值相同，仅事务边界不同

- 文档承诺：`Corrections` 的注释（query.go:34）称返回 "every value the
  system ever believed for that point"；`Correction` 的注释（fact.go:38）称
  每条记录 "which value the system believed, and during which transaction
  interval it believed it"。
- 复现输入：
  `Write(e,p,"old",[10,20),tx=1)`；`Write(e,p,"new",[13,16),tx=2)`；
  `Corrections(e,p,validAt=11)`（落在左残差 `[10,13)` 内）。
- 实际输出：
  `[{old,TxFrom=1,TxTo=2}, {old,TxFrom=2,TxTo=0}]` —— 两条相邻条目值相同，
  仅 `TxFrom`/`TxTo` 边界不同（见 `TestCorrectionsPhantomAdjacentEntries`）。
- 应当输出：`[{old,TxFrom=1,TxTo=0}]`。在 validAt=11 这一点上，系统从 tx=1
  起一直相信值是 "old"，tx=2 的写入并未改变该点的取值，不构成一次修正。
- 根因分析：劈开时旧 fact 被关闭（`TxTo=txAt`），残差作为一条**新 fact**
  以 `TxFrom=txAt` 重新断言同一个值（store.go:99-122）；`Corrections`
  （query.go:41-58）只是把覆盖该点的所有 fact 原样倒出排序，不做相邻同值
  合并，于是内部的存储切分细节泄漏为一次从未发生的"值变更"。下游审计若按
  "相邻条目值不同才算修正"消费，会把每次劈开误报为一次纠正。

## 2. 残差的 TxFrom 是劈开（关闭）时刻，而非原始写入时刻

- 文档承诺：`Fact` 注释（fact.go:7）称 `TxFrom` 是 "the system knew this"
  的起点；`Correction.TxFrom` 同理。但 `Write` 注释（store.go:55）又说残差
  "survive as residual facts known from txAt"——两处文档本身互相矛盾。
- 复现输入：同 finding 1，检查劈开产生的残差 fact（见
  `TestResidualTxFromIsSplitTime`）。
- 实际输出：残差 `[10,13)` 与 `[16,20)` 的 `TxFrom` 均为 tx=2（劈开时刻），
  而非 tx=1（原始写入时刻）。
- 应当输出：按 `Fact`/`Correction` 的语义，系统自 tx=1 起就知道残差区域的
  取值（它由原 fact 蕴含），信念起点应为 tx=1。
- 根因分析：`residualLeft`/`residualRight` 直接取 `TxFrom: old.TxTo`
  （store.go:106、store.go:119），而 `old.TxTo` 刚被赋值为本次写入的 txAt。
  该取法对 `AsOf` 是必要的（保证同一 validAt 在任意 txAt 只有一条可见
  fact），但它把"存储记录的生效时间"与"系统首次相信该值的时间"混为一谈，
  并随 `Corrections` 泄漏给审计方。

## 3. 连锁劈开使同值条目无限堆积，轨迹随写次数膨胀

- 文档承诺：同 finding 1，轨迹应刻画"值何时被知道、何时被纠正"。
- 复现输入：`Write("old",[10,20),tx=1)`；`Write("n1",[13,16),tx=2)`；
  `Write("n2",[11,14),tx=3)`；`Corrections(validAt=10)`。
- 实际输出：`[{old,1,2}, {old,2,3}, {old,3,0}]` —— 三条同值条目
  （见 `TestCorrectionsAfterSplit/chained_splits_stack_same-value_entries`）。
  残差被再次劈开时会再关闭、再生成新残差，每多一次无关重叠写，轨迹就多一
  条幻影条目。
- 应当输出：`[{old,TxFrom=1,TxTo=0}]`；该点取值从未变化。
- 根因分析：劈开逻辑对残差与原始 fact 一视同仁（store.go:74-88 的循环不
  区分二者），每次重叠写都产生新一代 `TxFrom=txAt` 的同值残差；
  `Corrections` 不去重，污染随写入次数线性累积。

## 4.（次要）`residualLeft` 注释 "Boundaries are copied exactly" 具有误导性

- 文档承诺：store.go:98 称残差 "Boundaries are copied exactly"。
- 实际行为：仅 **valid** 边界被精确复制；事务边界并未复制——`TxFrom` 取的
  是 `old.TxTo`（关闭时刻）而非 `old.TxFrom`。
- 应当输出：注释应限定为 valid 边界，或说明事务边界的取值规则。
- 根因分析：注释措辞过宽，读者易据此推断残差完整继承原 fact 的时间区间，
  从而对 `Corrections` 轨迹中残差条目的 `TxFrom` 产生错误预期。

## 附注

- `Corrections` 注释承诺 "strictly increasing TxFrom"：在本实现下成立
  （每次劈开要求严格递增的 txAt，残差与新 fact 的 valid 区间互不相交），
  现有测试已覆盖，未发现反例。
- 以上均为 characterization 结论，未改动任何实现文件；修复方向（如
  `Corrections` 合并相邻同值条目、或残差继承原始 `TxFrom` 并调整可见性
  判断）需另行评估对 `AsOf` 语义的影响。
