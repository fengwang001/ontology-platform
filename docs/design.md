# 跨链接双时态溯源查询子系统 — 设计说明

包路径：`ontology/bitemporal`（module `ontology`，Go 1.26，无第三方依赖）。
可运行示例：`cmd/provenance-demo`。

## 1. 要解决的问题

在本体图上，源对象 `S` 经链接 `L` 到目标对象 `T` 的一条路径是否在
「给定有效时间点 `validAt`、给定写入时间点 `asOf`」下可见，必须同时满足：

1. `S`、`L`、`T` 各自被选定版本的有效时间区间（左闭右开）覆盖 `validAt`；
2. `S`、`L`、`T` 各自被选定版本的写入时间不晚于 `asOf`；
3. 三方缺一不可，不得只核对其中两方。

并支持限定深度的多跳遍历、可追溯的区间修正、两类不可见原因的区分、
固定次序的错误判定、并发可串行化、以及候选规模相对图总规模的局部有界。

## 2. 数据模型（只追加的版本日志）

- `ObjectRecord{ID, VersionID, Valid [From,To), WrittenAt}`
- `LinkRecord{ID, VersionID, SourceID, TargetID, Valid [From,To), WrittenAt}`

同一实体（对象或链接）的每次修正都是**追加一条新记录**，旧记录永不删除、
永不原地修改。写入约束：

- 同一实体的 `WrittenAt` 必须**严格递增**（修正的写入时间严格晚于被修正记录）；
- `VersionID` 在实体内唯一；
- 链接的端点属于链接身份，跨版本必须一致（端点不可被「修正」）；
- 区间非空：`From < To`。

端点不可变是刻意取舍：允许端点修正会使「同一条链接在历史时刻指向谁」
产生歧义（是覆盖旧事实，还是引用了另一个事实？）。端点变化应建模为
「旧链接失效 + 新链接建立」两条链接，而不是一条链接的版本演进。

## 3. 双时态可见性判定

对实体的版本序列（已按 `WrittenAt` 升序）：

1. 取 `WrittenAt <= asOf` 的**最后一条**为「截至该写入时间点已落定的
   最新版本」。修正语义为「整体替换当前认知」：新版本一旦落定，它对
   历史有效时间的描述即成为当前认知（bitemporal correction 模型）。
2. 单个参与方返回三态：
   - `Visible`：存在已落定版本且其区间覆盖 `validAt`；
   - `NotEstablished`（此刻未建立）：**所有**版本（不论写入时间）的
     区间都不覆盖 `validAt`；
   - `NotYetVisible`（已建立但对此查询尚不可见）：至少存在一条覆盖
     `validAt` 的记录，但它（们）写入更晚，已落定版本不覆盖。
3. 一跳综合三方状态：任一参与方 `NotEstablished` 则整跳
   `NotEstablished`；否则任一为 `NotYetVisible` 则整跳 `NotYetVisible`；
   三方全部 `Visible` 才成边。该优先级写入 `combineStatus` 并在文档
   中固定，`BlockedEdge` 同时保留三方各自状态供调用方自行归类。

边界语义：区间左闭右开，`validAt == To` 不算覆盖，`validAt == From`
 算覆盖（`interval_test.go` 专测）。

### 关于「重放历史查询结果可能变化」的诚实说明

纯追加日志下，在修正发生*之前*发出的 `(validAt, asOf)` 查询看到的是
旧世界；在修正发生*之后*重放同一查询，路径集合不变（晚写入的版本被
`asOf` 排除），但不可见**原因类别**可能从 `NotEstablished` 变为
`NotYetVisible`——因为系统现在掌握了一条未来写入的新知识。路径集合
（即题面要求「完全相同」的对象）始终稳定；阻断原因是对「未来知识」的
诊断信息，这一取舍在 `TestCorrectionBoundaryAndRepeatability` 中显式断言。
若业务要求原因类别也冻结，需要在查询时持久化结果快照，属于另一种
（成本更高的）设计，见第 8 节。

## 4. 多跳遍历

- 按深度 BFS，`maxDepth` 为跳数上限，只接受正整数。
- 深度每增加一跳，对该跳的链接与目标对象**重新**执行三方判定；
  对象解析结果在单次查询内缓存（同一双时态点结论唯一）。
- 任一跳不通过：该跳及其延伸路径不产生结果，但更浅的、已通过的路径
  照常返回；失败边以 `BlockedEdge` 形式返回（含三方各自状态与综合类别）。
- 不延伸回当前路径已含节点（消除环导致的无限延伸）；按完整节点序列
  排序输出并按 (节点序列, 链接序列) 去重，使结果与 map 遍历、调度次序
  完全无关——这是「不随查询执行先后次序改变」的确定性保证。

## 5. 错误的固定判定次序

`QueryError.Kind` 四类互斥，代码中按以下次序短路返回，且有
`TestErrorPrecedence` 锁定：

1. `ErrSourceNotFound`：源对象无任何写入记录；
2. `ErrInvalidTime`：`validAt` 或 `asOf` 为零值；
3. `ErrInvalidDepth`：`maxDepth <= 0`；
4. `ErrAsOfBeforeEarliestWrite`：`asOf` 早于源对象最早写入时间。

次序的含义：实体存在性先于参数合法性（对不存在的实体谈论参数没有
意义）；时间合法性先于深度；最后才是「参数合法但早于该实体第一条历史」
这一语义错误。

## 6. 并发模型与可串行化

- `Store` 用一把 `sync.RWMutex` 保护全部 map：追加写入取写锁；
  一次查询通过 `Snapshot()` 取读锁，在**同一把读锁覆盖的整个 BFS
  期间**读一致性快照，结束时 `Release()`。
- 因此查询要么完整看到某条修正记录、要么完全看不到，不可能看到
  「区间已换、写入记录未落定」的中间状态；写入串行化点即追加点，
  查询串行化点即快照点，整体等价于某个全局顺序（读已提交/单版本快照
  隔离在「仅追加、从不变更」模型下即严格串行化）。
- 实现注意（`snapshot.go` 注释）：Go 的 `sync.RWMutex` 不支持同
  goroutine 重入——有写者等待时二次 `RLock` 会自死锁；所以快照方法
  直接读 map，绝不在持锁期间再次 `RLock`。

## 7. 候选规模的局部有界

存储维护按源端点组织的出链索引 `out[sourceID] -> []linkID`。单次
`(source, maxDepth)` 查询只访问：

- 深度 ≤ maxDepth 的可达子图内对象的版本链；
- 这些对象的出链版本；

与图中对象/链接总数无关，即 O(可达子图规模) 而非 O(全图)。
`TestCandidateBoundedByReachableSubgraph` 以可复现方式核对：固定可达
子图（4 条边），噪声子图从 200 增至 12800 个不连通对象/链接，
`Stats{ObjectsResolved, LinksConsidered, HopsChecked}` 恒为
`{5,4,4}`。`Stats` 由查询引擎在遍历过程中实际计数（非估算），可在
日志与测试中直接核对。

## 8. 关键取舍与被放弃的方案

- **放弃 B 树式时间索引/区间树**：版本链短、按 `WrittenAt` 线性选取
  已落定版本即可；复杂索引增加并发不变量与测试面。可在版本链变长时
  再替换为二分（接口不变）。
- **放弃端点可修正**：见第 2 节，改为新旧两条链接。
- **放弃查询结果物化冻结**：题面要求路径集合稳定，纯追加 + asOf 过滤
  已满足；冻结原因类别需要额外的结果存储与保留策略，留作扩展点。
- **放弃全局 MVCC 版本号**：只追加数据天然不可变，单一 RWMutex 的
  快照即提供串行化，无需事务 ID 与垃圾回收；高吞吐场景可换 MVCC。
- **朴素参照实现刻意不用任何索引**（全量线性扫描、现场分组），且与
  索引实现共用同一份 `asOfView` 判定/遍历代码（通过 `graphView`
  接口），从结构上保证两者语义只能一致。

## 9. 查询日志

`Logger` 接口在每次查询（含失败查询）后记录 `QueryLogEntry`：
输入 `Query`、`*Result`（路径集合 + 每条路径每跳的三方版本标识
`HopEvidence`）与错误。`MemoryLogger` 为并发安全的内存实现，生产中
可替换为落盘实现。测试 `TestQueryLogging` 校验三方记录标识齐全。

## 10. 本地验证

```bash
# 全量（含竞态检测）
GOCACHE=/tmp/gocache go test -race -count=1 ./...

# 随机差分（24 种子 × 800 步 ≈ 1.9 万次逐条对照朴素实现）
go test -run TestRandomDifferentialAgainstNaive -v ./bitemporal

# 有界性可复现核对（含规模/计数日志）
go test -run TestCandidateBounded -v ./bitemporal

go vet ./... && gofmt -l .
go run ./cmd/provenance-demo
```

测试矩阵对应题面要求：

- 区间修正前后边界（左闭右开、validAt==From/To）：`interval_test.go`、
  `TestCorrectionBoundaryAndRepeatability`；
- 链接写入晚于/早于两端对象：`TestLinkWriteTimeBothSides`；
- 多跳中间一跳恰好不满足：`TestMultiHopMiddleHopFails`；
- 并发修正与查询交织：`TestConcurrentCorrectionsAndQueries`（`-race`）；
- 朴素实现大量随机对照：`TestRandomDifferentialAgainstNaive`；
- 日志含输入、路径集合、三方记录标识：`TestQueryLogging`；
- 有界性可验证可复现：`TestCandidateBoundedByReachableSubgraph`；
- 写入严格递增、端点一致、结果确定性：其余用例。
