# 分代孤儿回收子系统 — 设计说明

包路径：`ontology/orphan`（Go 1.26，零三方依赖）。

## 1. 要解决的问题

对象是否为「孤儿」取决于其入边，而链接类型对保留判定有两种且仅有两种贡献方式：

- **独立保留（Independent）**：存在任意一条该类型入边即非孤儿；
- **联合保留（Joint）**：必须与另外一个或多个特定类型的入边**同时存在**才共同保留，
  单独存在不构成保留。

孤儿不立即删除，先进第一代队列，满第一代宽限期仍为孤儿则转入第二代并**重新计时**，
第二代宽限期（可与第一代不同，通常更短）满仍为孤儿才真正清理。宽限期内重新获得
「足以使其不再是孤儿」的入边即撤销待回收身份并清空判定记录。

## 2. 关键模型与取舍

### 2.1 两层核对，次序固定

`retainLocked` 严格分两层：

1. **第一层**：按固定（排序后）顺序逐个核对已配置的独立类型，命中即返回
   `reason="independent:<type>"`，**不再进入第二层**；
2. **第二层**：仅在第一层未命中时执行。逐个联合分组核对该组是否「所有成员类型
   都至少有一条入边」，第一个完整组命中即返回 `reason="joint:<group>"`；
3. 两层均未命中才是孤儿。

「脱离待回收队列」不设单独标准：新增/删除入边后调用的是**同一个** `retainLocked`，
因此脱离判定与初始判定完全同标准（测试 `TestEscapeUsesSameRuleJoint` 直接验证了
半组联合类型不能让第二代对象脱离）。

### 2.2 联合类型如何归一为「分组」

配置形如 `partOf requires [locatedIn]`、`locatedIn requires [partOf]`，也可能是
`a requires [b,c]` 这类非对称声明。为使判定规则不依赖声明方向，构造时在「联合类型
之间」对 requires 图做**无向连通分量**（union-find），每个连通分量是一个保留组：
组内**每一种**成员类型都至少有一条入边时，该组才完整。

联合类型的 requires 中引用独立类型也合法（第 3 类校验只要求被引用类型存在）：
独立边本就总能单独保留，这种引用在判定上不会产生新结果，但会作为该组的附加要求
（`jointGroup.extras`）参与核对并被记入日志，保证「据以判定的入边组合」可审计。

### 2.3 状态机：单字段三代

每个对象只有一个 `gen` 字段：`NoGen(0) / Gen1(1) / Gen2(2)`，从结构上杜绝
「同时属于两个队列」。

```
        变孤儿                         第一代宽限期满
NoGen ───────────▶ Gen1(since=now) ─────────────────────▶ Gen2(since=now)
  ▲                    │                                      │
  │ 重新获得保留入边     │ 重新获得保留入边（仍用同一判定）         │ 重新获得保留入边
  └────────────────────┴──────────────────────────────────────┘
                        回到 NoGen，since 清零，无任何代际记忆
Gen2 宽限期满 ──▶ 原子清理（级联处理自身出边）
```

要点：

- 任何一代中只要重新判定为「保留」，直接回到 `NoGen` 并清空 `since`；
  第二代对象**不**退回第一代，也不保留任何记忆——下次变孤儿一律从第一代起算
  （`TestGen2RelinkNoMemory` 在 t=1000 验证重新从第一代计时）。
- 第一代内脱离后再次变孤儿，`since` 取当前时刻重新计满第一代，不延续已过时长
  （`TestGen1RelinkCancelsAndRestarts`）。
- 推进边界是 `now-since >= grace`：恰好到期推进，差 1ms 不推进
  （`TestGraceBoundaryExactVsJustBefore` 覆盖 9/10、10/10、14/15、15/15）。

### 2.4 判定成本不随入边总数增长（可复现证明）

入边按 `type -> set<src>` 两级 map 存储。判定一次只对「类型存在性」做探测：

- 第一层探测次数 = 已配置的独立类型数（命中即止）；
- 第二层探测次数 = 各组成员类型数（逐组短路）。

同类型入边再多，也只是一个 `len(set)`，不产生额外探测。`RetainReport` 直接把
`IndependentChecks / JointGroupsTried / JointMemberChecks` 暴露出来，测试
`TestCheckCountIndependentOfEdgeCount` 在 fan-in=50 与 fan-in=5000 两种规模下断言
探测次数完全相同（独立命中恒定为 1、0；最坏孤儿情形恒定为 1、1 组、2 次成员探测），
从而以可在测试中复现的方式给出证明。日志里的 `PresentCounts` 仍完整记录各类型
实际入边条数，供审计，但它不参与判定探测。

### 2.5 并发：单把锁 + 变更即时重判 = 可线性化

所有变更（建对象、加/删边）与扫描共用一把互斥锁，且每次变更在锁内**立即**对受影响
对象重判。任何并发历史都等价于这些操作的某个全局串行序列：

- 对象不可能同时在两个队列（单 `gen` 字段）；
- 清理在同一把锁内完成「判定过期 → 级联处理出边 → 删除对象」，外部读不到
  「判定已过期但队列未更新」的中间态；
- 时间来自可注入的 `Clock`（生产用墙钟，测试用原子实现的 `ManualClock`）。

被放弃的方案：

- **每代一条独立队列 + 两把锁**：对象可能在两条队列里出现中间态，且跨队搬迁需要
  两阶段提交，复杂且易错；单字段 + 单锁以极小代价直接消除该类错误。
- **惰性扫描时才判定孤儿**：会让扫描成为重操作且使「首次判定时刻」不明确；
  改为变更时即时重判，扫描只负责到期推进，职责单一。
- **维护全局入边计数器并逐条核对**：判定 O(入边总数)，不满足复杂度要求；放弃。

### 2.6 清理的原子性与级联删除的边界

级联删除的既有规则不在本题范围内重新规定，因此定义接口
`CascadeDeleter.DeleteCascade(cx *Cascade, id)`：清理时在锁内调用一次，`Cascade`
提供 `Outbound / RemoveEdge` 等锁定上下文。默认实现 `DefaultCascade` 按确定顺序
删除全部出边（删边会即时重判目标对象，因此链式影响在同一原子区间内收敛）。
应用方注入自己的实现即可接入既有规则；无论何种实现，「对象删除」整体对外原子。

### 2.7 四类错误互斥、次序固定

固定优先级：

1. `ErrObjectNotFound`（目标实例不存在；dst 先于 src 核对）
2. `ErrTypeNotConfigured`（链接类型未配置任何贡献方式）
3. `ErrUndefinedJointRef`（联合配置引用未定义类型）
4. `ErrNonPositiveGrace`（宽限期非正数）

配置期错误由 `validateConfig` 按 2→3→4 定序返回，且对 map 做了排序以消除
Go map 迭代随机性；实例错误（1）在边操作入口最先核对。
`TestErrorOrdering` 逐组断言多个错误同时存在时只报优先级最高的一类。

### 2.8 日志

每次判定与每次代际推进都追加一条 `LogEntry`：输入（时刻、对象、各类型实际入边
组合与计数）、判定过程数据（两层各自的探测次数）、输出（是否保留、原因、
队列代际迁移、判定时刻）。除内存环形留存（`System.Logs()`）外可注入外部
`Logger` 同步观测。

## 3. 朴素对照模型

`NaiveModel` 是**独立另写**的参考实现：显式保存全部边，每次变更后扫描整个边集
重建每个对象的判定（O(全部边)），联合分组也在本地重新做 union-find，不共享
`System` 的任何判定/分组代码。差分测试对两者喂同一条随机操作流，**每一步**都
逐条比对存活集合、每个对象的代际与计时器、两代队列内容与错误类别。

## 4. 本地验证方法

```bash
# 本环境 Go 位于 /tmp/goshim/go（等价 /usr/local/go，GOCACHE 在 /tmp）
/tmp/goshim/go test -race -count=1 ./...

# 仅本包，带详细输出
/tmp/goshim/go test -race -v ./orphan

# 只跑随机差分 / 复杂度证明 / 并发
/tmp/goshim/go test -race -run 'RandomDifferential|CheckCount|Concurrent' ./orphan

# 格式化与静态检查
gofmt -l .
/tmp/goshim/go vet ./...
```

测试索引：

| 需求点 | 测试 |
| --- | --- |
| 两层核对次序 | `TestIndependentRetention`、`TestJointRetentionAndLayerOrder` |
| 恰好到期/刚过去临界 | `TestGraceBoundaryExactVsJustBefore` |
| 第一代脱离后重新起算 | `TestGen1RelinkCancelsAndRestarts` |
| 第二代脱离且无记忆 | `TestGen2RelinkNoMemory` |
| 脱离与初始同标准 | `TestEscapeUsesSameRuleJoint` |
| 级联出边 + 原子清理 | `TestCleanupCascadesOutboundEdges` |
| 四类错误定序互斥 | `TestErrorOrdering` |
| 探测次数不随入边增长 | `TestCheckCountIndependentOfEdgeCount` |
| 日志输入/输出/入边组合 | `TestDecisionLogContents`、`TestExternalLogger` |
| 并发交织、不可同属两代 | `TestConcurrentMutationsScansAndCleanup`（-race） |
| 朴素模型大规模随机对照 | `TestRandomDifferentialVsNaive`（40 个种子 × 1500 步） |
