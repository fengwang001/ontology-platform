# 事件溯源网络重建与追溯孤儿判定 — 设计说明

## 1. 目标与范围

本子系统在一条只追加的事件流上重建“对象 + 类型化链接”网络在任意历史时刻
`T` 的状态，并在此基础上判定某个对象在时刻 `T` 是否“实质上已成孤儿”：

> 按判定所声明依据的级联清理规则版本，该对象的**全部必需入链**在该时刻
> 都已被撤销，因而本应被级联处理；但事件流中并不存在明确的
> `OrphanMarked` 记录。

这种“本应孤儿、却没有显式事件”的对象被标记为 **追溯孤儿
（retroactive orphan）**，区别于流中真正出现过 `OrphanMarked` 的
**级联孤儿（cascade orphan）**。

## 2. 事件与全序

事件类型（`ontology/model.go`）：

- `LinkTypeDeclared`：声明链接类型（全局 schema）。
- `ObjectCreated`：创建对象，携带对象类型。
- `LinkEstablished` / `LinkRevoked`：建立 / 撤销类型化边。
  边方向固定为 `ObjectID -> PeerID`，`LinkType` 为类型；必需性针对
  **入链**（`To == 判定对象`）。
- `PropertyAssigned`：属性赋值（对象的正常活动）。
- `OrphanMarked`：显式的级联清理记录。

每条事件的全序键为 `OrderKey{Time, Seq}`：先按业务时间 `Time`，
同刻以全局提交序号 `Seq` 作为权威 tie-breaker。经 `Store.Append`
提交的事件不会产生并列；并列只可能来自外部导入（`ImportStream`），
并在判定时作为 `ErrAmbiguousOrder` 报告。

### 2.1 必需链接与孤儿点

规则版本（`RuleVersion`）为不可变值：

- `ID`：不可变版本标识，一旦安装永不作废（只新增、不回改）。
- `EffectiveFrom / EffectiveFromSeq`：成为 HEAD 的时刻；版本严格
  单调向前，禁止回退（`AdjustRule` 对更早版本返回错误）。
- `Requirement: 对象类型 -> 必需入链类型列表`；不在表中的对象类型
  永远不会成为孤儿。
- `Retroactive`：本版本是否追溯适用生效点之前的撤销。

对每种必需入链类型，判定窗口内独立维护：

- `inCount`：当前活跃必需入链数；
- `everActive`：窗口内是否至少活跃过一次（“从未拥有必需链接”的对象
  不算孤儿，其要求未被“撤销”，而是从未满足）；
- `clearedAt`：该类型最近一次从非零降为零的撤销位置；之后若又建立
  新链接，则清零并撤销先前的孤儿点。

**虚拟孤儿点**在完整重放到 `T` 之后统一计算：当对象在 `T` 仍存在、
每种必需类型都满足 `inCount == 0 && everActive` 时，取所有类型
`clearedAt` 的最大值（最后一个必需类型也被清空的那次撤销）。这保证：

- 再建立必需链接会“撤回”早先的虚拟孤儿点；
- 再次全清空时得到新的、更晚的点；
- 判定结果是 `(事件流, 对象, T, 规则版本)` 的纯函数。

## 3. 追溯方向的唯一规定（不因请求时间而翻转）

判定永远只依据**请求显式声明的规则版本**（`Basis`）：

- `Basis{VersionID: id}` 钉死某个不可变版本：无论之后 HEAD 怎样调整、
  请求何时发起，对同一 `T` 的结论完全一致（幂等）。
- `Basis{}`（HEAD）表示“在本次请求最终落定那一刻仍为当前版本”的版本：
  计算前记录 HEAD 的 `EffectiveFromSeq`，在排他最终化点复核；若期间
  HEAD 已移动，则以 `ErrRuleSuperseded` 失败（请求方可改为显式钉版重试）。

由此，“依据旧版还是依据追溯新版”完全由 `Basis` 决定，方向对所有历史
时刻保持一致，不会因调用时机不同而对同一 `(T, VersionID)` 给出不同结论。

### 3.1 追溯版 vs 非追溯版

- `Retroactive=true`：判定窗口 `[创世, T]`。发生在版本生效点之前的撤销
  也按本版本评估，因此可以在比版本生效点更早的时刻产生虚拟孤儿点。
- `Retroactive=false`：判定窗口 `[EffectiveFrom, T]`。生效点之前已被撤销
  的链接被祖父保留——生效时刻的既有状态作为基线，只有窗口内新发生的
  “先建立后撤销”才可能触发孤儿；生效点之前从未建立、窗口内才建立并撤销
  的链接仍正常计数（窗口内成立即可）。

两种情形的边界穷举见 `ontology/ruleversion_test.go`。

## 4. 追溯孤儿但后续仍有活动

系统绝不把追溯孤儿等同于级联孤儿。重建输出（`ObjectState`）为每个对象
给出三类互斥状态：

- `StatusActive`：在钉死版本下仍存活；
- `StatusCascadeOrphan`：流中存在 `OrphanMarked`，报告 `MarkedOrphanAt`；
- `StatusRetroactiveOrphan`：规则上应被级联但无标记，报告 `VirtualOrphanAt`。

对孤儿点之后仍出现的正常事件（属性赋值、新建链接），系统**不删除、不
改写**，而是在 `ActivityAfterVirtual`（追溯孤儿）与 `ActivityAfterMarked`
（级联孤儿）中逐条列出，供上层决定如何呈现（例如“应已被清理却仍有写入”
的合规告警）。验证见 `presentation_test.go`。

## 5. 错误分类与优先级

四类互不相同的错误（`ontology/errors.go`），同一次请求同时满足多类时，
只报告固定优先级中最高的一类：

1. `ErrRuleSuperseded`：声明依据的规则版本在处理期间被作废（HEAD 移动；
   或钉死的版本标识未安装 / 尚无任何版本）。
2. `ErrDanglingReference`：使用了未声明链接类型、或先于声明使用；引用了
   尚未创建的对象。
3. `ErrBeforeFirstAppearance`：请求时刻早于对象首次出现时刻。
4. `ErrAmbiguousOrder`：相关前缀内存在相同 `OrderKey`、先后无法确定。

实现上先收集候选错误再用 `pickError` 按优先级返回（见
`adjudicate.go`）。**任何错误路径都不写入审计、不追加事件、不修改规则
版本指针**（错误零可观察改动由 `errors_test.go` 校验审计条数与流长度）。

全网 `Rebuild` 不因子对象错误而整体失败：各对象错误进入
`NetworkState.Problems`，其余对象正常重建。

## 6. 并发与可串行化

`Store` 用一把 `sync.RWMutex` 串行化所有变更（事件追加 / 导入 / 规则调整），
并以 **copy-on-write 快照**发布不可变 `snapshot`：

- 变更者在写锁内克隆将改动的 map，替换快照指针；已发布快照永不就地修改。
- 判定在读锁下取快照指针后无锁运行，期间追加/改规则不影响本次计算。
- 最终化点再取写锁复核 HEAD 是否仍匹配；HEAD 漂移即返回
  `ErrRuleSuperseded`，审计也在该点写入。

因此任意一组并发操作的可观察结果都等价于某个全局串行序：每个提交
（事件 `Seq`、规则 `EffectiveFromSeq`）在写锁下取得唯一顺序，判定则
等价于在其最终化点被串行执行。`-race` 下的交错压测见
`cost_test.go::TestConcurrentSerializability`。

## 7. 判定成本不随全网链接事件总量线性增长

索引维护每个对象自己的事件切片 `byObject[id]`（边事件同时进入两端切片）。
判定只扫描该对象切片的 `T` 前缀，`DetermineResult.EventsScanned`
精确报告本次扫描条数。该数字与网络中其它对象累计的创建/撤销量无关，
仅随**该对象自身**的相关事件数增长。

可独立验证的证明方式（`cost_test.go`）：

- 对同一目标对象，分别在网络中注入 100 与 10000 组无关对象的
  建立/撤销，断言两次 `EventsScanned` 完全相等（=3：创建+建立+撤销）。
- 同时运行独立朴素模型 `NaiveReplay`，它每次从头线性扫描**全流**；
  审计中的 `CrossCheck.EventsScanned` 随全网事件量增长，且严格大于
  索引路径扫描量，形成可复现实验的“索引成本 O(对象自身事件)、朴素成本
  O(全网事件)”对照。

权衡：交叉校验本身是全流线性成本，因此 `DetermineRequest.SkipCrossCheck`
允许在性能基准/热路径上显式关闭；常规判定默认开启并记录对照结论。

## 8. 独立朴素模型与审计

- `NaiveReplay`（`naive.go`）是独立实现的朴素逐事件重放：不共享索引、
  不共享判定辅助函数，每次从原始事件切片拷贝、排序并线性回放，重建全局
  边集后给出结论。
- `runCrossCheck` 比较朴素结论与索引结论（状态、是否有虚拟点、是否有
  标记、虚拟点位置），不一致时在审计中保留两份结论及差异说明，绝不静默
  覆盖。
- 每次成功判定写入一条 `AuditRecord`：输入（对象、`T`、解析后的 `Basis`）、
  依据的规则版本（ID、生效序号、是否追溯）、结论、索引扫描条数、当时流
  高水位 `Seq`、以及朴素对照结论。随机差分测试逐条校验审计完整性。

## 9. 被放弃 / 未采用的方案

- **规则就地修改（mutable rule）**：放弃。会使同一历史时刻的结论随时间
  翻转，直接破坏幂等与“方向恒定”。改为不可变版本 + 只增 HEAD 指针。
- **允许规则版本回填（backdate）**：放弃。会让旧时刻的 HEAD 语义不确定；
  `AdjustRule` 强制新版本严格排序在后，追溯能力由 `Retroactive` 标志表达，
  与生效时刻解耦。
- **判定全程持有写锁**：放弃。虽天然可串行化，但把只读判定与追加完全
  串行，吞吐差。改为 COW 快照无锁计算 + 最终化点复核，冲突才升级为
  `ErrRuleSuperseded`。
- **在追加时就拒绝悬空引用**：放弃。要求写入端先见 schema/对象，无法
  表达乱序到达的外部日志；改为追加宽容、判定时按相关前缀精确报错。
- **用时间戳唯一排序、同刻任意选边**：放弃。会产生不可重复的结论。
  追加路径以 `Seq` 为权威 tie-breaker；导入路径保留并列并显式报
  `ErrAmbiguousOrder`。
- **把虚拟孤儿点做成“首次全清零即锁存”**：放弃（实现中曾采用并被测试
  暴露）。再建立必需链接后无法撤回早先点，`TestReestablishmentResets...`
  与随机差分均会失败；改为按类型记录 `clearedAt` 并在重放后取最大值。

## 10. 本地验证方法

仓库根目录（Go 1.26.5）：

```bash
# 如 go 不在 PATH：
export PATH=/usr/local/go/bin:$PATH
# 若默认 GOCACHE 只读：
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath

gofmt -l .
go vet ./...
go test ./...                 # 含默认交叉校验
go test -short ./...          # 降低随机差分采样密度
go test -race ./...           # 并发可串行化 + 数据竞争
go test -run TestDeterminationCostIndependentOfGlobalStreamSize -v ./...
go test -run TestRandomDifferential -v ./...
```

测试覆盖：

- 追溯 / 非追溯规则的边界穷举（含同刻、窗口外、祖父保留、多必需类型、
  从未拥有链接、再建立重置）；
- 追溯孤儿后续活动的呈现规则与级联孤儿的区分；
- 随机操作序列下索引判定与独立朴素重放的逐条对照及审计记录；
- 四类错误的单独触发、同刻多错误的优先级、错误零副作用；
- 判定成本相对全网事件量不增长的可复现实验；
- 并发追加 / 规则调整 / 钉版判定的 `-race` 串行化压测。
