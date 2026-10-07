# 跨链接双时态溯源查询子系统 — 设计说明

包路径：`ontology/provenance`（Go，无第三方依赖）。

## 1. 问题模型

- 每个对象实例与每条链接实例各拥有两类时间：
  - **有效时间区间** `[Start, End)`，左闭右开；`Start == End` 为空区间。
  - **写入时间** `WriteAt`，表示该条认知被落定的时刻。
- 修正（收窄、延长、整体替换、消亡、复活）= **追加一条写入时间严格更晚的新记录**。
  旧记录永不修改或删除。链接两端端点不可变；端点变更建模为删旧链、建新链。
- 一次 AsOf 查询给定 `(Source, ValidAt, AsOf, MaxDepth)`。
  路径「源对象 →链接→ 目标对象」可见，当且仅当源对象、该链接、目标对象
  三方各自在 `(ValidAt, AsOf)` 下均可见，缺一不可。

## 2. 单方双时态可见性判定

对同一实体按写入时间有序的记录序列，判定规则（对象与链接完全相同）：

1. 取 `WriteAt <= AsOf` 中写入时间最大的记录（asOf 时刻已落定的最新认知，**含墓碑**）：
   若它 `Exists` 且区间覆盖 `ValidAt` ⇒ `visible`；
2. 否则，若存在 `WriteAt > AsOf` 且 `Exists` 且区间覆盖 `ValidAt` 的记录
   ⇒ `not_yet_visible`（已建立，但对本次查询尚不可见）；
3. 否则 ⇒ `not_established`（此刻未建立）。

关键细节：第 1 步选取「最新记录」时必须把消亡记录（`Exists=false`）也算进去。
若最新认知是墓碑，则即便更早记录覆盖 `ValidAt` 也不可见——这正是朴素参照实现
在差分测试中暴露并修复过的缺陷（见 `naive.go` 修订记录对应的测试
`TestDifferentialVsNaive`）。

`not_established` 与 `not_yet_visible` 的区分让调用方能区分「这个时间点本来就
没有覆盖认知」与「认知未来才写入，asOf 尚不可见」。

## 3. 多跳遍历

- BFS 按深度 `1..MaxDepth` 扩展；每跳先判定链接、再判定链接当前指向的目标对象，
  失败方固定为 `link` 先、`target` 后，原因类别随结果返回（`RejectedHop`）。
- 任一跳不可见则该跳及其延伸路径被剪枝；更浅深度上已核对通过的前缀路径
  照常作为结果返回。
- 环只在**单条路径**上阻断（避免 A→B→A→… 无限遍历），不做跨路径全局去重，
  因而不同链接构成的平行路径都会独立返回。
- 结果（路径、拒绝项）在返回前按节点序列 / `(from, link)` 字典序排序，
  保证输出与插入顺序、遍历调度次序无关；无新写入时重复查询结果逐字节一致
  （`TestRepeatableReads`）。

## 4. 错误的固定判定次序

四类错误互斥，按下列次序只返回第一类，次序不随内部实现变化：

1. `ErrSourceNotFound`：源对象实例在存储中完全不存在（无任何写入历史）；
2. `ErrInvalidTime`：`ValidAt` 或 `AsOf` 为非法值（`IllegalTime = math.MinInt64`）；
3. `ErrInvalidDepth`：`MaxDepth <= 0`；
4. `ErrAsOfBeforeSource`：`AsOf` 早于源对象最早一条记录的写入时间。

注意：源对象「存在但在此刻不可见」不是错误，而是返回空路径集合加
`SourceReason`（`not_established` / `not_yet_visible`），供调用方区分。

## 5. 并发与可线性化

- 存储用单一 `sync.RWMutex`。写操作（追加记录、更新邻接索引、分配全局 `Seq`）
  在同一写临界区内完整完成；查询全程持读锁，看到的是某一个提交点的一致状态。
  因此查询不可能观察到「链接区间已被修正、但对应写入时间记录尚未完整落定」
  的中间状态；读写操作的任一真实交织都等价于按全局提交顺序串行执行。
- `Seq` 是全局单调提交序号，仅用于日志追溯与同写入时间记录的稳定决胜，
  不参与双时态判定（写入时间被要求严格递增，同刻写同一实体会被拒绝）。
- `TestConcurrentWritesAndQueries` 在 8 写者 × 8 读者 × 200 轮交织下，配合
  `go test -race` 验证无竞争、无未来写入泄漏、无半条修正引用。

## 6. 候选路径复杂度

- 维护 `source → []LinkID` 的邻接索引，索引中保留历史上存在过的全部链接
  （包括后来被墓碑修正的链接），因为可见性只能由查询按 asOf 判定，
  索引不能按「当前状态」裁剪历史。
- 一次查询触及的候选链接数
  `CandidatesSeen = Σ(被访问节点的出链数)`，只取决于 `MaxDepth` 与被访问
  子图的邻接规模，**与全图对象/链接总数无关**。
- `TestCandidatesIndependentOfGraphSize` 以可复现的方式证明：固定查询子图，
  将无关干扰子图从 100 节点扩到 2000 节点，主实现 `CandidatesSeen` 恒为 2；
  同一用例下朴素全扫描实现的计数从 502 线性增至 4002，作为对照证明该指标
  确实度量了「是否扫描全图」。

## 7. 审计日志

`QueryLogger` 输出 JSON Lines，每次查询一行：输入参数（source/valid_at/
as_of/max_depth）、源记录引用、每条返回路径及其每一跳所依据的链接记录与
目标记录标识（`id + write_at + seq`）、被剪枝的跳及原因、候选计数。
错误查询用 `LogError` 单独成行。日志器内部互斥，保证并发交织下行不撕裂。

## 8. 关键取舍与被放弃的方案

- **整数时间刻度而非 `time.Time`**：让「非法时间值」有唯一、可测的哨兵
  `math.MinInt64`，错误分类的第 2 类可确定性触发；真实接入时在适配层换算。
- **单把 RWMutex 而非分片锁 / MVCC**：在本规模下语义最简单、可形式化论证
  可线性化；放弃了更高写吞吐方案，因为它们会引入版本可见性与「半落定」
  的复杂正确性问题。接口未暴露锁，后续可在不改调用方的前提下替换实现。
- **查询全程持读锁而非拷贝快照**：append 只追加、旧切片不原地改写，读锁内
  遍历是安全的；放弃显式快照拷贝以避免每查询复制全表。
- **链接端点不可变**：避免同一链接身份的图拓扑被静默改写造成历史追溯歧义。
- **路径环只按单条路径阻断**：放弃全局访问去重，因为全局去重会漏掉平行
  路径（同一目标经不同链接到达应分别成路径）。
- **返回全部深度前缀而非仅最深路径**：更浅的已核对路径是合法溯源结论，
  剪枝只影响「失败跳及其之后」，不影响更浅结果（与需求一致）。

## 9. 本地验证方法

```bash
export GOCACHE=/tmp/gocache          # 若默认缓存目录只读
go test ./...                         # 全量
go test -race -count=3 ./provenance   # 竞态 + 重复稳定性
go test -coverprofile=cov.out ./provenance && go tool cover -func=cov.out
go vet ./... && gofmt -l .
```

重点测试：

- `TestIntervalBoundaryHalfOpen`：validAt 恰落修正前后边界（150 同时是旧右端/
  新左端），覆盖「未建立 / 尚不可见 / 可见」三态。
- `TestLinkWrittenBeforeObjects` / `TestLinkWrittenAfterObjects`：链接写入时间
  早于 / 晚于两端对象两种情形。
- `TestMultiHopMidFailure`：中间一跳恰在边界不满足，浅层结果保留。
- `TestConcurrentWritesAndQueries`：并发修正与查询交织（配合 `-race`）。
- `TestDifferentialVsNaive`：40 个随机种子 × 80 条随机修正 × 120 个随机查询，
  与独立朴素实现逐条对照。
- `TestCandidatesIndependentOfGraphSize`：复杂度承诺的可复现证明。
- `TestQueryLoggerContents`：日志含输入参数、路径集合与每跳三方记录标识。
