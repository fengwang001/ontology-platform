# 链接基数约束与批量导入原子性 —— 设计说明

## 1. 模块划分

实现全部位于 `ontology` 包，按职责拆分为四个内聚模块：

| 文件 | 职责 |
| --- | --- |
| `schema.go` | 链接类型声明、两端基数上限（`ExactlyOne` / `AtMostOne` / `AtMost(n)` / `Unlimited`）、有序实例对、声明期校验 |
| `ledger.go` | **基数账本**：维护每个具体实例在每个链接类型每一侧的已占名额；`check` / `apply` / `release` |
| `store.go` | 链接存储：以 `(链接类型, 起点, 终点)` 为键的去重集合 |
| `service.go` | **批量导入与事务编排**：单条创建/删除、分批提交、全有或全无回滚、单一互斥临界区 |
| `errors.go` | **错误归一化**：四类可相互区分的错误及固定判定次序 |
| `batch.go` | 批量语义（`AllOrNothing` / `BestEffort`）与逐条结果清单 |
| `naive.go` | 朴素对照模型（测试资产）：只存全量集合、每次校验都重新遍历计数 |

## 2. 关键数据结构与不变量

### 2.1 基数账本（`ledger.go`）

账本不是「按链接类型存全部链接再计数」，而是两份计数器表：

```
source: (linkType, instance) -> 当前作为起点占用的名额
target: (linkType, instance) -> 当前作为终点占用的名额
```

不变量：对任一时刻的链接集合 `L`，

```
source[(t,x)] = #{ (t, x -> *) ∈ L }
target[(t,y)] = #{ (t, * -> y) ∈ L }
```

该不变量在随机测试中由 `assertLedgerMatchesSet` 用全量集合重数逐条对账。

### 2.2 校验与变更只做常数次计数器访问

- `check` 固定读取 2 个计数器（起点一个、终点一个），与该对象类型下链接总数无关；
- `apply` / `release` 固定写 2 个计数器；
- 计数归零时 `delete` 键，使 map 规模只与「当前非零实例数」相关，而不是历史链接数。

**可验证的证明方式**：`probeCounter` 探针累计 `check` 中发生的计数器访问次数。
`TestCheckCostIndependentOfTotalLinks` 在 10 条与 1000 条链接两种规模下各发起
一次创建，断言探针增量都恰好为 2。这比大 O 论证更强：它是对「访问次数不随
数据规模增长」的直接机器测量。

### 2.3 两端独立、只报第一个命中原因

`check` 同时返回 `sourceExceeded`、`targetExceeded` 两个独立布尔值（即使起点
已超限也照常完成终点判定）。service 层按固定次序决定对外错误：

```
KindInvalidArgument（类型/实例不存在、类型不匹配、有序对重复）
  → KindSourceCardinality
  → KindTargetCardinality
```

任一端超限则整条链接不落存储、不改账本——判定与写入严格分离，被拒条目零痕迹。

## 3. 批量导入语义

两种语义共用同一个执行循环，差异只在失败后的动作：

- 按列表顺序逐条处理；每接受一条立即 `store.add` + `ledger.apply`，
  **后续条目直接读到累积后的账本**，因此批内占用是累积的，而非基于批次前快照。
- 批内另维护 `seenPairs`：同一有序实例对在输入中第二次出现即为参数非法；
  与已存储链接重复同样为参数非法。
- **BestEffort**：失败条目标注原因后继续，不影响后续；返回顺序一致的完整清单。
- **AllOrNothing**：第一条失败即停止，按接受顺序的逆序成对执行
  `store.remove` + `ledger.release`，存储与账本回到批次前状态；清单中失败点
  给出真实原因，其后未处理项标记为「批次已中止」。整批在一次锁内完成，外部
  不可能观察到中间态。

逆序回滚不影响正确性（各计数器独立加减，交换律成立），选择逆序只是与「逐条
撤销」的直觉一致，便于审计。

## 4. 并发与删除可见性

`Service` 用**一把互斥锁**串行化所有变更（创建、删除、两语义批量）。

- 这直接保证「效果等价于某个全局串行顺序」：临界区顺序就是该串行顺序。
- 不会出现两个并发创建都读到「尚余一个名额」后双双成功——读判定与写占用在
  同一临界区内完成。
- 删除在释放两个计数后才退出临界区，后续获得锁的创建立即看到空闲名额，
  **释放可见时刻与删除生效时刻严格一致，无额外时延**。
- 批量导入整体持锁，因此一批与其他创建/删除之间也存在确定的串行先后。

## 5. 删除不存在的链接

`store.remove` 返回 false 时归一化为 `KindNotFound`；未知链接类型同样按
`NotFound` 处理（语义为「要删的链接不存在」）。它与
`KindSourceCardinality` / `KindTargetCardinality`（链接存在性无问题、只是
名额已满）是不同的 `ErrKind`，调用方可用 `errors.As` 稳定区分，无需解析文本。

## 6. 关键取舍与被放弃的方案

1. **增量计数器 vs 每次重数全量集合**：采用计数器表，得到与链接总数无关的
   校验开销；放弃了「每次遍历计数」的朴素做法（仅保留在 `naive.go` 作为差分
   测试的判定依据）。代价是必须维护账本与集合的一致性，由删除/回滚成对操作
   与随机对账测试兜底。
2. **单把全局锁 vs 每实例细粒度锁/令牌桶**：单锁最容易证明可串行化，且本系统
   的瓶颈定位是大批量导入的语义正确性而非吞吐。放弃了按实例分片加锁（会显著
   复杂化批次回滚与删除竞争的推理）；未来若需要扩展，可在 service 接口不变的
   前提下替换为按键分区的有序加锁。
3. **批量在一次锁内完成 vs 分段提交**：放弃分段提交，因为全有或全无要求外部
   永不观察到中间态，单临界区天然满足。超长批次可在 API 层分批，每批仍保持
   原子边界。
4. **批内临时账本 vs 批次影子计数**：直接复用同一账本 + 逆序回滚，而不是维护
   独立的批次影子计数。这样批内累积与跨操作可见性只有一套语义，不会出现影子
   与主账本合并时的边界错误。
5. **`ExactlyOne` 的处理**：创建路径上 `ExactlyOne` 与 `AtMostOne` 同为
   「上限 1」。「恰好一个」的存在性义务属于对象生命周期收口约束，不在创建
   API 的职责内；`Cap()` 的文档明确写清这一取舍。
6. **有序对去重键包含链接类型**：不同链接类型可连接同一对实例；同一类型下
   `(A,B)` 与 `(B,A)` 视为不同链接。
7. **错误只用枚举、不带机器可读载荷**：拒绝判定只依赖固定次序，附加信息放入
   错误文本供人读，机器分支只看 `ErrKind`，避免调用方依赖文本格式。

## 7. 本地验证方法

```bash
# 需 Go 1.26+；若 go 不在 PATH：export PATH=$PATH:/usr/local/go/bin
# 若默认构建缓存只读：export GOCACHE=/tmp/gocache-ont

go test ./...                       # 全量测试
go test -race -v ./...              # 竞态检测 + 打印输入/输出/判定依据
go test -cover ./...                # 当前覆盖率约 90%
go vet ./... && gofmt -l .          # 静态检查与格式
```

测试矩阵（均打印输入、实际输出与判定依据）：

| 测试 | 覆盖点 |
| --- | --- |
| `TestBatchCumulativeRejectsLater` | 批内累积导致后序条目被拒，而其在批次前快照下本应合法 |
| `TestTwoSemanticsDiffer` | 同一输入下全有或全无（整批回滚为空）与尽力而为（留 3 条）的差异 |
| `TestBoundaryExactAndOverByOne` | 占用恰好等于上限合法、再多一个被拒 |
| `TestDeleteThenConcurrentCreate` | 删除释放后立即被并发创建抢占；两种可线性化结局都被接受 |
| `TestConcurrentContendedCreate` | 64 并发抢 1 名额：恰好 1 成功、63 起点超限、实际占用不超发 |
| `TestCheckCostIndependentOfTotalLinks` | 10 条 vs 1000 条链接下单次校验计数器访问恒为 2 |
| `TestRandomDifferentialVsNaive` | 40 个种子 × 300 个随机操作（创建/删除/两种批量）逐条对照朴素模型，并对新实例重放同一序列验证确定性 |
| `TestRandomTracePrinting` | 固定种子逐操作打印输入、实际输出与朴素模型判定依据 |

## 8. 对外 API 速览

```go
s := ontology.NewService()
s.RegisterLinkType(ontology.LinkTypeDecl{
    Name: "owns", SourceType: "Person", TargetType: "Dog",
    SourceCap: ontology.CardinalityBound{Kind: ontology.Unlimited},
    TargetCap: ontology.CardinalityBound{Kind: ontology.ExactlyOne}, // 每只狗恰有一个主人
})
s.RegisterObject("p1", "Person")
s.RegisterObject("d1", "Dog")

err := s.CreateLink("owns", ontology.Pair{Source: "p1", Target: "d1"})
ontology.KindOf(err) // KindOK / KindInvalidArgument / KindSourceCardinality / ...

s.DeleteLink("owns", ontology.Pair{Source: "p1", Target: "d1"}) // 不存在 -> KindNotFound

s.ImportLinks(ontology.BestEffort, []ontology.BatchItem{...})   // 逐条结果，顺序一致
s.ImportLinks(ontology.AllOrNothing, []ontology.BatchItem{...}) // 全有或全无
```
