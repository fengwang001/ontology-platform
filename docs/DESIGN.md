# Action 前置/后置校验职责分离机制 — 设计说明

## 1. 目标与术语

一个 Action（动作）的一次调用（call）经历两个独立校验阶段：

- **前置校验（pre）**：只能依据**调用开始前已经持久化**的对象与链接状态，
  不得观察本次执行产生的、尚未提交的任何写入。
- **后置校验（post）**：只能依据**本次执行计划写入的最终结果**（对执行前
  状态叠加最终写入计划后的投影状态），不得重新评估前置阶段已经通过的条件。

四类对外暴露的结果，优先级从高到低：

1. `definition_error`（声明自相矛盾，**定义期**判定，优先于一切运行期结果）
2. `object_revoked`（目标对象执行期被并发撤销）
3. `pre_rejected`（前置不通过，返回**全部**不通过条件）
4. `post_rejected`（后置不通过，只返回**决定性的一个**条件）

`object_revoked` 与 `pre_rejected` 的相对优先级本实现固定为
**撤销优先**（题面允许实现自定，但要求对相同输入稳定）。

## 2. 核心模型（`actionguard/model.go`）

- 所有判定都表达为**字面量（Literal）**：`AtomSpec`（原子判定模板）+ 极性
  `Expect bool`。一个命名条件（`ConditionClause`）是若干字面量的合取；
  一个动作的某阶段通过，当且仅当该阶段声明的全部字面量成立。
- `AtomSpec` 的实参是 `ConstArg`（声明期常量）或 `VarArg`（执行期由输入
  绑定的变量）。同一种模板表示既服务执行期求值，也服务定义期矛盾检查。
- `Snapshot` 是不可变只读视图，仅暴露：
  - `Atom(grounded)`：布尔原子判定；
  - `Attr(id, attr)`：供 Planner 读取执行前属性；
  - `Version(id)` / `Revoked(id)`。
- `Plan` 是**最终值**写入计划：`ObjectAttrs` / `ObjectRevoked` / `Links`
  全部是“目标键 → 最终值”的映射。`PlanBuilder` 的重复 `SetAttr`/`SetLink`
  直接覆盖，因此同一次调用内对同一对象的多次写入在结构上只保留最终值，
  中间状态不可能进入计划，也就不可能被后置校验看到。

## 3. 两阶段的判断依据如何在结构上隔离

- **前置隔离**：`Executor.Execute` 进入 Store 临界区后首先深拷贝出
  `base immutableSnapshot`（`Store.snapshotLocked`）。前置条件与 Planner
  只拿到 `base`；Planner 签名为 `func(input, snap Snapshot) (*Plan, error)`，
  **不接收 Store**，因此无法产生任何可观察副作用，也无法偷看未提交写入。
- **后置隔离**：全部计划生成完毕后，`Store.project(base, plan)` 从 `base`
  的再深拷贝出发叠加**最终计划**，得到唯一的投影快照。所有后置条件都在这
  **同一个**投影快照上求值。后置求值函数签名中没有执行前快照，类型层面即
  无法重评前置条件；并且投影快照是计划生成完成后一次性构建的，不存在
  “先产生的写入用早快照、后产生的写入用晚快照”的可能。

## 4. 定义期矛盾检测（`actionguard/define.go`）

题面要求：声明的条件集合在给定输入下无法被满足为一个自洽的通过集合时，
必须在动作定义阶段暴露，而不是等到执行期。

做法：同一阶段的所有字面量两两做**模板可合一性（unification）**检查：

- 两个字面量 `kind` 相同、实参数相同，且逐位置 `Const-Const 相等`、
  变量可一致绑定（并查式替换表）时，二者可在某组输入下落到同一个原子；
- 若此时极性相反，则任何输入下二者不可能同时成立 → 声明自相矛盾，
  `Register` 返回 `*DefinitionError`，动作不会注册、永远无法执行。

跨阶段（前置 vs 后置）的字面量**故意不**纳入同一检查：两个阶段的判断依据
本来就是不同时间点的状态（执行前 vs 计划后），同一原子在两点取值不同是
正常语义（例如余额在执行前 `>=x`、执行后变为新值），合并检查会产生大量
伪矛盾。这是有意的取舍。

### 开销不随历史调用次数增长（可验证）

- `checkConsistency(action *Action) error` 的**签名不包含 Store/Executor**，
  也不访问任何全局历史；Go 类型层面即可审计它无法读到调用记录。
- 时间上界：字面量数 n、实参数 k 时为 **O(n²·k)**，空间 O(n·k)。
- 验证手段：
  - 单测 `TestContradictionCheckCost`：先对同一矛盾声明检测一次，再让另一个
    动作产生 20000 次被接受调用后再次检测，两次都立即返回矛盾，结果与耗时
    不随历史变化；另用 400 个互异 kind 的字面量验证自洽集合可通过。
  - 基准 `BenchmarkCheckConsistency`（lits=50/200/800）实测耗时按 ~n² 增长
    （约 40µs / 43µs / 689µs），与调用次数无关。

## 5. 执行协议（`actionguard/executor.go`）

每次 `Execute` 都在**同一个 Store 临界区**内顺序完成：

1. 取执行前深拷贝快照 `base`；绑定环境变量（输入、`target`、
   `targetsSorted`，以及自动绑定的全集 `allIdsSorted`）。
2. 撤销检查：任一目标在 `base` 上 `Revoked` → `object_revoked`，审计留痕，
   不产生写入。
3. 前置校验：**无条件求值全部前置条件**（即使前面的已经失败），收集所有
   失败名，稳定排序后一次性返回。任何前置失败 → 直接返回，
   不进入计划阶段。
4. Planner 纯函数生成最终计划。
5. 一次性投影得到最终快照；按声明顺序逐个求值后置条件，**命中第一个失败
   即停止**（题面要求的不对称性），该条件写入：
   - 普通审计轨迹（每次校验的输入、依据、结论）；
   - **独立失败轨迹** `FailureTrail`（仅供事后审计，不物化为任何对象，
     不参与对象状态、版本号、序号或历史序列）。
   计划引用随即丢弃，效果等价于从未尝试。
6. 全部后置通过才 `commitPlan`：此刻才为每个被写对象 `version++` 并追加
   对象历史与全局接受序列（`AcceptedCalls`）。提交后写 post-commit 审计。

因此：

- 前置拒绝：零状态变化、零版本/序号消耗、零对象历史/动作类型历史；
- 后置放弃：计划整体丢弃，除独立失败轨迹外与从未尝试完全等价；
- 版本号单调、且与对象历史长度严格相等（测试断言）。

## 6. 并发正确性（线性化）

`Store.mu` 是一把覆盖“快照→前置→计划→后置→提交”的互斥锁。每个调用在
临界区内看到的都是某个已提交状态，其接受/拒绝决定与提交在该临界区内
原子完成。因此所有调用天然构成一个**全序**：并发执行的被接受集合及其
效果，等价于按该全序逐一串行执行。这直接排除了“两个调用都基于旧快照
通过前置、组合后破坏后置不变量”的异常交织。

### 被放弃的并发方案

- **每对象锁 / 两阶段加锁**：转账涉及多对象，需要死锁规避与多锁原子快照，
  且后置不变量常常跨全部对象（如总量守恒），最终仍要退化为全局序；复杂度
  高而收益有限，放弃。
- **OCC（版本号CAS + 重试）**：重试会让一次逻辑尝试消耗多个序号、且与
  “失败不得消耗版本号/序号”的要求冲突；放弃。
- 若未来吞吐成为瓶颈，可按不变量分片（不相交对象集的动作并行），接口不变。

## 7. 其他被放弃的方案

- **后置条件直接读 Store**：会让后置看到执行期间其他并发调用的提交状态，
  破坏统一快照视角，放弃；改为只读投影快照。
- **计划携带增量事件流**：中间状态会泄漏给后置校验，也使“同对象被同调用
  后续步骤覆盖”需要额外折叠逻辑；改为最终值映射，折叠在 PlanBuilder 内
  自然完成。
- **失败也写对象历史（标记 tombstone）**：违反“失败不入对象/动作类型
  历史序列”，放弃；失败只进独立审计轨迹。

## 8. 审计轨迹

`AuditEntry{CallID, ActionType, Phase, Condition, Input, Basis, Result}`：

- 前置：每个条件一条，含输入 JSON 与逐字面量依据（实例化原子、观测值、
  期望值）；
- 后置：决定性条件（失败时）或全部条件（接受后 post-commit 摘要）；
- 撤销：一条 revocation 记录。
- 后置失败额外进 `FailureTrail`。单测核对了轨迹包含“前置通过 + 后置失败”
  的完整依据（`TestPrePassPostFail_StateInvariant`）。

## 9. 测试与本地验证

全部位于 `actionguard/`：

| 测试 | 覆盖点 |
| --- | --- |
| `TestPrePassPostFail_StateInvariant` | 前置通过/后置失败后余额、版本、历史完全不变；失败轨迹恰有一条且带依据 |
| `TestPreFail_AllFailuresAndStateInvariant` | 多个前置同时失败全部返回；零版本、零历史、零失败轨迹 |
| `TestContradictoryDefinition_RejectedAtRegistration` | 定义期拒绝；尝试执行不产生任何审计与状态变化 |
| `TestDefinitionErrorTakesPrecedence` | 声明矛盾优先于一切运行期类别 |
| `TestAcceptedPath` / `TestObjectRevoked` | 接受路径版本/历史；撤销类别与状态不变性 |
| `TestPostSeesFinalPlanNotIntermediate` | 同对象计划被同调用覆盖时后置只看最终值 |
| `TestConcurrentVsNaiveSerial` | 400 个并发随机交织调用，对照独立朴素串行实现：接受**序列**、每对象余额/版本/历史逐项一致；余额守恒、无负余额 |
| `TestConcurrentFeesVsSerial` | 300 个手续费动作并发（含前置通过后置失败）与串行重放最终状态一致 |
| `TestConcurrentWithRevocation` | 并发撤销下守恒、版本/历史一致、撤销位保持 |
| `TestContradictionCheckCost` | 2 万次历史调用前后矛盾检测结果不变 |
| `BenchmarkCheckConsistency` | 检测成本随声明字面量数（~n²）而非历史增长 |

```bash
export GOCACHE=/tmp/gocache          # 若默认缓存目录只读
go test ./...
go test -race -count=5 ./...         # 竞态 + 重复稳定性
go test -run=NONE -bench CheckConsistency -benchmem ./actionguard
go vet ./... && gofmt -l .
go run ./cmd/demo                    # 四类路径的可观察演示
```

## 10. 目录结构

```
actionguard/model.go     字面量/快照/计划/结果/审计类型
actionguard/store.go     持久化对象、深拷贝快照、最终计划投影、提交、轨迹
actionguard/define.go    定义期矛盾检查（合一）、条件求值
actionguard/executor.go  执行协议（撤销→前置→计划→后置→提交）
actionguard/bank.go      示例动作：转账、带费提现、自相矛盾动作
actionguard/*_test.go    不变性、并发串行对照、开销验证
cmd/demo/main.go         端到端演示
```
