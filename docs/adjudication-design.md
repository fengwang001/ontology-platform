# 备份重建裁决组件设计说明

包路径：`ontology/adjudicator`

## 问题与目标

本体平台的备份按四类数据独立存放：对象类型定义、对象实例、链接实例、
动作执行记录，各自可能整体缺失、整体损坏或部分损坏。裁决组件回答两个问题：

1. 以何种**唯一确定**的顺序重建，才能使任意中间状态内部自洽
   （任何一类数据不会在其依赖之前被标记为已重建）；
2. 每一类备份整体不可用时，其余各类数据可被恢复到什么范围。

## 职责划分

组件按"单类内部判定"与"跨类依赖核对"两条职责线划分，外加执行期会话：

| 部分 | 文件 | 职责 |
| --- | --- | --- |
| `assessor` | `adjudicator/assessor.go` | 单类备份内部可用范围判定：整体是否可用、哪些记录自身可恢复。不看任何跨类依赖。 |
| `evaluator` | `adjudicator/evaluator.go` | 跨类依赖核对：在各类别可用范围之上沿依赖链判定每条记录是否可重建。 |
| `resolveOrder` | `adjudicator/order.go` | 对可重建子图做确定性拓扑排序，产出唯一重建顺序。 |
| `Adjudicator` | `adjudicator/adjudicator.go` | 裁决入口：编排上述部分，产出 `Verdict`（顺序 + 逐条判定 + 判定依据日志 + 最高优先级错误）。无状态、纯函数。 |
| `Session` | `adjudicator/session.go` | 重建执行：按序推进，处理中途新发现的损坏。 |

数据流：`Snapshot → assessor（各类别 scope）→ evaluator（SCC 求值）→
resolveOrder（确定性拓扑序）→ Verdict`。

## 关键设计取舍

### 依赖图求值：强连通分量而非朴素递归

记录间的依赖构成有向图（边从记录指向其依赖）。`evaluator` 用 Tarjan
算法求强连通分量，把每个分量当作整体判定：分量可重建当且仅当分量内
每条记录自身可恢复、无悬空依赖（指向备份中不存在的记录）、且依赖的
分量全部可重建。Tarjan 按汇优先顺序产出分量，依赖分量天然先被处理，
一次遍历同时完成可重建性传播与循环检测。

被放弃的方案：

- **带"访问中"标记的朴素递归 + 记忆化**：在环上会产生与遍历起点相关
  的不一致结果（同一记录在环不同入口下记忆化出不同值），需要额外
  迭代至不动点才能收敛，反而更复杂。
- **逐边反复传播的松弛法**（类似 Bellman-Ford）：实现简单但最坏
  O(V·E)，且无法自然识别"除循环外本可重建"的记录，循环检测还要
  再跑一遍。

### 唯一顺序：固定平局裁决规则

拓扑排序用最小堆（`container/heap`）做 Kahn 算法，平局键为固定的
二元组 **(类别优先级, 记录 ID 字典序)**，类别优先级固定为
`类型定义 < 对象实例 < 链接实例 < 动作记录`。该规则不依赖任何运行时
状态，因此同一输入永远产出同一顺序，重复裁决与并发裁决结果完全一致
（由 `TestDeterministicRepeatedAdjudication`、`TestConcurrentAdjudication`
与对照测试中的重复裁决断言共同保证）。

### 只读输入与无状态裁决器

`Adjudicator` 不持有任何可变状态，每次裁决是 `Snapshot` 的纯函数；
`Session.ReportCorruption` 需要重新评估时，构造带新损坏标记的
**快照副本**而非修改原快照。因此并发调用安全（`-race` 验证），
且可用 gob 摘要在测试中断言快照逐字节不变。

### 中途新发现损坏：停止而非撤销

`Session` 维护 `pending / outstanding / done / blocked` 四个集合。
`ReportCorruption` 在快照副本上重新裁决：已完成（`done`）的记录
一律保留不撤销；未完成的记录按新裁决结果重新划分——仍可按原顺序
推进的留在 `pending`，变为不可重建的移入 `blocked` 并标记
`ReasonLateCorruption`。这保证"立即停止依赖该备份的后续动作，
已完成部分不撤销，依赖关系全部重新评估"。

### 错误分类与固定优先级

四类错误互不相同，判定顺序固定：

1. **整体不可用**（`ErrKindCategoryUnavailable`）：输入的固有属性，
   是其余一切判定的前提——类别整体缺失/损坏时，其记录与全部下游
   直接判死，无需再看部分损坏与顺序。
2. **部分不可用级联**（`ErrKindCascade`）：只有整体可用的类别才谈得上
   部分损坏；级联是整体可用前提下的次生问题，故次之。
3. **循环依赖**（`ErrKindCycle`）：循环检测只在剔除不可重建记录后的
   可重建子图上才有意义（环上若有损坏记录，应归因于损坏而非循环），
   故再次之。
4. **中途新发现损坏**（`ErrKindLateCorruption`）：只发生在执行阶段，
   静态裁决中不存在，故优先级最低，由 `Session` 单独报告。

单条记录的原因码（`ReasonCode`）与之一一对应，并额外区分
`ReasonSelfDamaged`（自身损坏）与 `ReasonDependencyUnavailable`
（依赖级联），判定依据写入 `Verdict.Log`。

### 边界判定：可恢复范围是精确集合

`assessor` 输出的可恢复范围是精确的记录集合（损坏集合的补集），
不存在"边界模糊"状态。动作记录等下游记录的判定逐依赖核对：
任一依赖落在可恢复范围之外即整体不可重建，且 `BlockedBy` 精确
列出落在范围外的依赖。不允许出现"不确定"的中间判定
（`TestBoundaryDependency` 验证）。

## 判定开销的可复核证明

要求：判定任意一条记录的开销不随备份总规模失控增长，只与其依赖链
长度相关。

机制：

- `BuildIndex` 一次性构建只读索引（O(总规模)），可摊销到任意多次查询；
- `CheckRecordIndexed` / `evaluator.evaluate(roots)` 只从目标记录出发
  沿依赖边遍历可达子图，不触碰无关记录；
- `evaluator` 内置 `edgeVisits` 计数器，记录判定过程中实际访问的
  依赖边条数，使开销**可复核**而非仅口头承诺。

证明方式（`adjudicator/complexity_test.go`）：

- 固定链长 k=64，总规模从 1k 增至 50k（50 倍），`edgeVisits` 严格
  不变且等于链上边数 k−1；
- 链长翻倍至 2k，`edgeVisits` 随之线性变为 2k−1。

即单条记录判定开销 = O（依赖链上的边数），与总规模无关。

## 测试策略

| 测试文件 | 覆盖点 |
| --- | --- |
| `adjudicator_test.go` | 四类备份各自整体缺失/整体损坏；部分损坏的级联与完好部分推进；跨四类依赖链级联；边界依赖；循环检测；错误优先级；重复裁决确定性；并发一致性与输入只读。 |
| `differential_test.go` | 500 个固定种子随机构造的损坏组合（含悬空依赖、同类内依赖），与朴素逐条递归参照模型逐记录对照；校验顺序合法性、重复裁决一致性；把每次裁决的输入、输出与判定依据写入 `adjudicator/testdata/differential_log.jsonl`。 |
| `complexity_test.go` | 上述开销证明；损坏链上的单条判定。 |
| `session_test.go` | 正常推进顺序与裁决一致；中途损坏的停止/不撤销/重评估；重复报告损坏的幂等。 |

## 本地验证方法

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 只看对照测试与开销证明
go test ./adjudicator/ -run 'TestDifferential|TestCheckRecord' -v

# 代码检查
gofmt -l .
go vet ./...
```

对照裁决日志：`go test ./adjudicator/ -run TestDifferentialAgainstNaiveModel`
后查看 `adjudicator/testdata/differential_log.jsonl`，每行一次裁决的
完整输入、输出与判定依据。
