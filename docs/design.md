# 嵌套动作调用：前置条件继承边界与后置效果传播规则

本文档说明本体平台中"动作触发动作"的执行语义设计，覆盖：前置条件的可见性边界、
失败传播与整体放弃、自我触发检测、输出依赖的静态拦截、并发链条的可序列化，
以及关键取舍、被放弃的方案与本地验证方法。

实现位于 `ontology/` 包：`state.go`（存储/快照/写入计划/分层视图）、
`action.go`（动作声明与注册期校验）、`engine.go`（嵌套执行与提交）、
`trace.go`（审计轨迹）。`cmd/server` 是最小 HTTP 演示。

## 核心模型

一条**调用链条**（chain）以一次根动作调用为入口，执行过程中可通过
`Context.Invoke` 触发嵌套调用，形成调用树。整条链条共享：

- 一个**持久化快照**（`Snapshot`）：链条开始时的一致性只读状态；
- 一棵**分层写入计划**（`WritePlan` 层栈）：每个嵌套调用一层，未提交；
- 一个**读集**（reads）：所有回落到快照的读取，用于提交时冲突检测；
- 一个**祖先键集合**（ancestors）：当前调用路径上的 `(动作, 规范参数)` 键；
- 一条**审计轨迹**（`Trace`）：每次调用的路径、前置/后置求值依据与结论。

求值视图 `View = 快照 + 写入计划层栈（内层优先）`。只有整条链条成功结束，
写入计划才会在存储锁内原子提交；任何整体放弃都只是"不提交"，存储天然零污染。

## 规则与实现对应

### 1. 前置条件的继承边界

- 内层动作的前置条件**独立重新评估**，与外层是否通过无关
  （`engine.go: runAction` 对每一层都完整执行 前置 → Run → 后置）。
- 内层前置条件求值所用的 `View` 叠加了外层当前已计算、尚未提交的写入计划
  （`childContext` 把父层计划压入层栈）。因此内层能看到外层的中间写入。
- 根动作的前置条件只看到持久化快照（其计划层为空），
  保持"单个动作前置校验只看持久化状态"的原有规则。
- 测试：`TestInnerPreconditionSeesOuterPendingWrites`（两个子用例分别覆盖
  嵌套可见中间写入、根调用只见持久化状态）、
  `TestInnerPreconditionReevaluatedIndependently`。

### 2. 失败传播：关键调用整体放弃，非关键调用记录后继续

- 每一层调用在独立的子写入计划上执行；**成功**才并入外层计划，**失败**整层丢弃。
- 关键调用失败（前置/执行/后置任一阶段）经 `abortError` 沿调用链向外传播，
  整条链条放弃提交——外层与内层已计算的写入计划一并丢弃，不存在
  "外层保留、只放弃内层"的中间态。
- 非关键调用失败只记录事件（`OutcomeNonCriticalFailed`），外层继续后续流程；
  `InvokeResult` 明确携带失败结论，后续流程不可能把它误认为成功。
- 测试：`TestCriticalFailureAbortsWholeChain`、`TestNonCriticalFailureContinues`。

### 3. 自我触发检测

- 触发前检查 `(动作, 规范参数)` 键是否已在当前调用路径的祖先集合中；
  命中即拒绝（`OutcomeSelfTriggerRejected`），**先于**内层前置条件求值。
- 拒绝不触碰任何已计算的写入计划，链条其余部分以拒绝前的状态继续向外传播
  （即使被拒绝的调用声明为关键调用，拒绝本身也不触发整体放弃）。
- 嵌套深度无预设上限：不同参数组合的同动作递归（如倒计时）可任意深度放行。
- 测试：`TestSelfTriggerDirectRejectedBeforePreconditions`（含"前置条件未被执行"
  的计数断言）、`TestSelfTriggerIndirectAtDepth`（深度 3 间接触发 + 同参数
  不同动作的放行边界）、`TestSelfTriggerBoundaryDifferentArgsAllowed`（深度 64 放行）。

### 4. 输出依赖的静态拦截

- 动作通过 `Calls` 静态声明嵌套调用、通过 `Consumes` 声明输出依赖。
- 注册期（调用链条确定时）校验：**任何对非关键调用输出的消费依赖都是声明错误**，
  直接拒绝注册，不会留到执行期才暴露。
- 运行期动态调用必须与静态声明一致，否则视为声明错误并整体放弃。
- 测试：`registry_test.go` 全部用例。

### 5. 并发链条的可序列化

- 链条在快照上并发求值，提交时在存储锁内原子完成：
  读集冲突检测 → 对象不变量校验 → 应用写入计划 → 分配提交序号。
- 读集冲突则整条链条基于新快照整体重试；因此所有已提交链条的效果
  **等价于按提交序号这一全序逐条串行执行**。
- 不变量在提交点针对最终写入对象校验，两条各自自洽的链条组合后若会破坏
  某对象的不变量，后提交者要么基于最新状态重试、要么被拒绝，
  不会出现组合效果违反不变量的提交。
- 测试：`TestConcurrentChainsSerializable`（48 条并发链条交织，
  与独立的朴素串行实现 `naiveSerialReplay` 按提交全序对照最终状态）、
  `TestInvariantViolationAbortsChain`。

### 6. 结果类别的区分与暴露

`Outcome` 枚举区分并暴露：`OutcomePreconditionFailed`（内层前置未通过）、
`OutcomePostconditionFailedCritical`（后置未通过且关键，整体放弃）、
`OutcomeNonCriticalFailed`（非关键失败，外层继续）、
`OutcomeSelfTriggerRejected`（自我触发拒绝），另加 `OutcomeOK` 与
`OutcomeRunFailedCritical`（执行期错误且关键，规格四类之外的补充类别）。

优先级：自我触发守卫固定在一切求值之前；其余按 前置 → 执行 → 后置 的
固定顺序短路，相同输入的结论稳定。每次调用在 `Trace` 中记录路径、深度、
前置/后置求值依据（快照版本 + 可见的未提交写入键）与最终结论，供事后核对。

### 7. 自我触发检测的开销上界

祖先键集合只保存**当前这一条调用链条的活动路径**：进入一层加入键、
退出即删除，每次触发前恰好一次 O(1) 哈希查表。因此单次检测的开销与
当前链条已发生的嵌套深度无关（常数），一条链条的总检测开销正比于其自身深度，
与系统历史上执行过的链条总量完全无关。

可验证方式：`Trace.GuardChecks()` 暴露本链条的查表次数，
`TestGuardCostBoundedByChainDepth` 断言：深度 32 的链条查表次数恒为 31，
且在额外执行 2000 条无关链条堆积历史后，再次执行同一链条查表次数不变。

## 关键取舍

- **分层写入计划 + 成功才并入**：内层失败（非关键）只需丢弃子计划，无需回滚日志；
  外层视图天然包含内层已成功的写入。代价是每层一次小的 map 分配。
- **关键失败用 panic 传播整体放弃**：`abortError` 为包内私有类型，在链条根部
  recover，用户 `Run` 代码无需显式检查每一次关键调用的返回值，也不会误恢复。
  代价是约定"用户代码不得 recover 该信号"，已在文档与注释中声明。
- **乐观并发控制（快照 + 读集校验 + 整体重试）**：读多写少、链条短时重试率低，
  且天然产出"提交序号"这一全序，便于测试对照与审计。写冲突极高的场景会退化
  为重试风暴，这是已知边界。
- **静态声明（Calls/Consumes）+ 注册期校验**：把"依赖非关键调用输出"这类错误
  左移到定义期；运行期只做声明一致性核对，开销可忽略。

## 被放弃的方案

- **全局锁串行执行所有链条**：正确性最直白，但完全消灭并发；仅在提交段持锁
  即可达到同样的可序列化效果，故放弃。
- **执行期才检查输出依赖**：拿不到输出时报错信息不可解释，且与"声明错误应在
  调用链条确定时拦截"的要求直接冲突，故放弃。
- **基于全局历史调用图的自我触发检测**：需要保存所有历史链条，开销随系统
  总执行量增长，违反"检测开销不超过当前链条深度"的约束，故放弃；
  改为只维护当前路径的祖先键集合。
- **内层失败立即落盘部分写入、事后补偿**：破坏原子性，补偿逻辑不可组合，
  与"整体放弃"语义冲突，故放弃；改为提交前写入计划完全私有。
- **两阶段提交/分布式事务**：当前为单存储进程内语义，引入 2PC 只会增加
  复杂度而不改变正确性结论，故放弃。

## 本地验证方法

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 关键路径单测
go test -run TestCriticalFailureAbortsWholeChain ./ontology
go test -run TestNonCriticalFailureContinues ./ontology
go test -run TestSelfTrigger ./ontology
go test -run TestGuardCostBoundedByChainDepth ./ontology
go test -run TestConcurrentChainsSerializable -race ./ontology

# 静态检查
gofmt -l .
go vet ./...

# 演示服务
go run ./cmd/server &
curl -X POST localhost:8080/v1/actions/transfer/execute -d '{"amount": 10}'
curl localhost:8080/v1/objects/acc
curl localhost:8080/v1/commits
```
