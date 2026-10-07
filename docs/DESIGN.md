# 对象类型版本迁移与运行中实例双写回填 — 设计说明

## 1. 目标与范围

对象类型从旧版本迁移到新版本期间，存量实例逐个异步回填。在全部回填完成
之前，旧版本与新版本两种结构的读写请求都必须返回与"迁移已完成"等价的
正确结果。系统由三个模块协作：

| 模块 | 文件 | 职责 |
| --- | --- | --- |
| 版本声明与兼容性判定 | `ontology/declaration.go` | 保存属性对应关系、修订序号、冻结集合；拒绝矛盾/越权修改 |
| 存量实例异步回填 | `ontology/backfill.go` | 稳定内部顺序的待回填队列 |
| 读写路由与一致性仲裁 | `ontology/store.go` | 全局串行化、新旧路由、等价转换、即时视图、回填仲裁 |

公共 API（`ontology.Store`）：

- `StartMigration(Migration)`、`AmendDeclaration([]Mapping)`
- `Create/Read/Write/Delete`（均带 `Version` 参数）
- `RunBackfill() BackfillOutcome`、`PendingBackfill()`、`IsBackfilled(id)`

属性对应关系有三种：`KindKeep`（保留）、`KindAdd`（新增，必须带默认值）、
`KindDrop`（废弃）。

## 2. 数据模型

每个实例只有一份物理数据，二选一：

- **未回填**：存旧结构 `oldRaw`；`backfilled=false`。
- **已回填**：存新结构 `newRaw`；记录转换时刻的声明修订序号
  `convertedAt` 与属性 Kind 快照 `kindsAtConversion`。

视图是读取时的投影，不是另一份数据，因此永远不存在"两份数据打架"：

- 未回填 + 旧读：直接返回 `oldRaw`。
- 未回填 + 新读：逐属性投影 —— `drop` 属性剔除，其余原样（即时现算，
  不等异步回填）。
- 已回填 + 新读：直接返回 `newRaw`。
- 已回填 + 旧读：用转换时刻快照投影 —— `add` 属性剔除。

## 3. 关键流程

### 3.1 旧结构写入未回填实例（需求第 2 条的"双写"）

旧结构写入不允许绕开新结构的约束与默认值规则，因此路径固定为：

1. 先按**当前声明**把旧结构等价转换成新结构（`keep` 原样、`drop` 剔除、
   `add` 补默认值），实例转为已回填形态；
2. 再把本次写入投影后覆盖到新结构（以转换结果为底合并，未显式提交的
   新增属性保留默认值）。

写入完成即视为回填完成，异步流程之后遇到它只会跳过。

新结构写入未回填实例时同样把实例翻转为新版本形态，但语义上是新版本下
的整体替换：只包含调用方提交的新结构属性，不注入旧值或默认值。

### 3.2 回填与并发（需求第 3 条）

`RunBackfill` 从队首取一项，在锁内检查实例当前状态：

- 实例已不存在（回填期间被删除）→ `Skipped, reason=deleted-before-backfill`；
- 实例已回填（被某次正常写入抢先）→
  `Skipped, reason=already-backfilled-by-write`；
- 否则执行一次"特殊写入"：转换 + 冻结本次用到的对应关系。

跳过都不是错误（不返回 error），既不覆盖也不复活。

**关键取舍**：实例因写入提前回填、或被删除时，**不**提前从队列摘除其
队列项。被放弃的方案是"状态变化即时删队列项"——那样回填流程根本观察
不到竞争发生，无法显式判定并报告"跳过而非覆盖/复活"，也削弱了对竞争
语义的可测试性。保留陈旧项后，跳过点唯一、可观测、可断言。
`PendingBackfill()` 因此按实例真实状态计数，而非队列长度。

### 3.3 声明追加与冻结（需求第 4、5 条）

- 一次请求内同一属性出现多次即视为自相矛盾（如同时 keep 与 drop），
  整体拒绝。
- 某属性一旦在任意实例的转换中被使用，进入 `frozen` 集合；之后对它的
  任何修改都拒绝。从未生效过的属性可任意追加/修订。
- 追加的新对应关系只影响尚未回填实例（在它们转换时读取当前声明）；
  已回填实例的旧视图只依据 `kindsAtConversion` 快照投影，不被二次回填。

校验在单锁内原子完成：全部通过才生效，任一非法则状态不变。

### 3.4 错误次序与错误类型（需求第 5 条）

每次操作固定先做参数校验，再查实例：

1. 参数非法 → 包装 `ErrInvalidArgument`（矛盾声明、修改已生效对应、
   通过错误版本写 add/drop 属性、未知版本等）；
2. 目标实例不存在 → 包装 `ErrNotFound`。

二者是不同的哨兵错误，`errors.Is(err, ontology.ErrInvalidArgument)` 与
`errors.Is(err, ontology.ErrNotFound)` 可明确区分。被拒绝操作不改实例
回填状态、不改任何版本下的可见数据（有专门测试断言）。

## 4. 并发与可串行化（需求第 6 条）

所有公共操作在同一个 `sync.Mutex` 下执行且持锁期间完成全部状态变更；
回填作为"一次特殊写入"与正常写入不可能半截交错。因此任何并发调度的
结果都等价于把这些操作按获取锁的顺序排成的某个全局串行序列，天然排除
了"回填与写入都成功却视图矛盾"。

确定性方面，回填队列的入队顺序在 `StartMigration` 时按实例 id 排序固定，
操作输出不依赖 map 迭代顺序。测试用固定随机种子生成操作序列，重放两次
比对逐条轨迹与最终状态（`TestReplayDeterministic`）。

被放弃的方案：

- **每实例细粒度锁 + 声明读锁**：并发度更高，但回填、声明冻结与队列仲裁
  跨越多把锁，需要额外的两阶段协议才能证明可串行化，复杂度与出错面显著
  增大。本子系统以正确性与可验证性为首要目标，单把锁足够；吞吐是后续
  按对象类型分片的演进项。
- **双写两份数据（old/new 各存一份）**：需要持续对账修复分歧，且无法
  从机制上杜绝双写窗口期的不一致。改为"一份物理数据 + 读时投影"后，
  视图矛盾在结构上不可能发生。

## 5. 即时现算视图的复杂度（需求第 7 条）

未回填实例的新读只遍历**实例自身属性**（O(实例属性数)），每个属性在当前
声明哈希表上做 O(1) 查询，从不扫描或重放任何历史修订；因此开销与历史
对应关系变更次数无关。默认值注入只发生在转换（写入/回填）路径，不属于
"读时现算"。

可验证手段（见 `ontology/proof_test.go`）：

- `TestReadViewCostIndependentOfHistory`：固定实例属性数，历史修订次数
  H = 0/100/200/400 时测量每次新读的堆分配数 —— 实测恒为 2，斜率为 0。
- `BenchmarkReadViewHistoryGrowth`：H = 100/1000/4000 时 ns/op 恒定在
  ~330ns 平台。
- `TestReadViewCostScalesWithInstanceAttrs`：历史固定、实例属性 4→256
  时每读分配数 2→4，证明唯一相关变量是实例属性数。

运行：

```bash
go test ./ontology/ -run TestReadViewCost -v
go test ./ontology/ -run '^$' -bench BenchmarkReadViewHistoryGrowth
```

## 6. 测试策略（需求第 8 条）

`ontology/` 下测试均通过 `t.Logf` 打印每条用例的"输入 / 实际输出 /
据以判定的依据"：

- `scenarios_test.go`
  - `TestBackfillRaceWithWrite`：写入抢先回填后，回填跳过不覆盖；
  - `TestBackfillRaceWithDelete`：删除竞争时跳过不复活，后续实例照常回填；
  - `TestAmendOnlyAffectsPending`：追加对应只作用于未回填实例，已生效
    对应修改被拒，已回填实例不被二次回填；
  - `TestViewsConsistentBeforeAfterBackfill`：回填前后新旧视图共有属性恒等；
  - `TestContradictoryAndErrorOrder`：矛盾声明、错误次序、拒绝不改状态。
- `naive_model_test.go` + `diff_test.go`：独立维护的朴素模型（记录实例
  实际版本、每次读取应用全部相关对应重新计算），60 个固定种子 × 700 个
  随机操作（发起/追加/矛盾修订/冻结修订/创建/新旧读写/删除/回填）逐条
  对照错误类别、读结果、回填结果与最终全量状态；并做重放确定性比对。
- `concurrency_test.go`：6 个 goroutine 真实并发混合读写、删除重建、回填、
  声明追加，`-race` 下校验收敛后无数据竞争、无新旧视图矛盾、无复活。
- `proof_test.go`：第 7 条复杂度的可执行证明。

## 7. 本地验证方法

```bash
go test ./...                         # 全量测试
go test -race ./...                   # 含竞态检测
go test ./ontology/ -v                # 查看逐条"输入/输出/依据"日志
go test ./ontology/ -run TestRandomDifferential -v
go test ./ontology/ -run '^$' -bench . -benchmem
gofmt -l . && go vet ./...
```

## 8. 已知边界与后续演进

- 单 Store 覆盖单个对象类型的一次 in-flight 迁移；多对象类型可各自建
  Store，天然分片。
- 单互斥锁以正确性优先；需要更高吞吐时可按实例 id 哈希分片，声明走
  `sync.RWMutex` + 不可变快照替换，保持同样的可串行化论证。
- 当前值类型为 `any`；接入真实类型系统时可在 `validatePayload` 处增加
  类型/必填校验，不影响迁移与回填主路径。
