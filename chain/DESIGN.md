# 动作嵌套调用：前置继承边界与后置传播规则 — 设计说明

实现位于 `chain` 包。本文说明关键取舍、被放弃的方案与本地验证方法。

## 1. 模型与术语

- **动作（Action）**：`Name + Pre + Effect + Post + Children`。子调用全部在
  `Children` 中**静态声明**（名字、关键性 `Critical`、触发条件 `When`、
  输入绑定 `Bind`、输出依赖 `Consumes`），动作函数体内不能“随手”触发调用。
- **帧（frame）**：一次动作触发对应调用栈上的一帧；一条顶层调用链条是一棵
  按声明顺序展开的帧树。
- **写入计划（WritePlan）**：`Effect` 与已完成关键子调用产出的 `WriteOp` 序列。
  执行期间不触碰持久化存储，整条链条成功后才在提交点一次性提交。
- **视图（View）**：`持久化快照 + 各层未提交写入计划` 的不可变叠加层，
  `View.Push` 返回新视图，绝不就地修改。

## 2. 前置条件的继承边界

- **内层前置必须独立重新评估**：外层前置通过与否对内层没有任何豁免效力。
  代码路径上每个帧都独立调用 `Action.Pre`（`executor.runFrame` 第 2 步），
  失败记 `StatusPreFailed`。
- **但内层看到的状态包含外层的中间写入计划**：内层进入视图由
  `view.Push(plan)` 构造，`plan` 是外层截至该次触发前已经计算出的全部写入
  （外层自己的效果与此前已完成关键子调用的写入），即使这些写入尚未提交。
  这与“单个动作内部前置校验只能看持久化状态”的差别正落在叠加层上：
  单帧 Pre 用的是**进入该帧时**冻结的视图，看不到本帧 Effect 此后才计算的写入；
  嵌套帧的进入视图天然叠加了外层计划。
- 测试：`TestInnerPreSeesOuterWritePlan`（外层先建对象未提交，
  内层 increment 的前置因此通过并读到 n=5）。

## 3. 后置效果传播：整体放弃 vs 非关键继续

- 失败来源统一为三类：前置未通过、`Effect` 返回 error、`Post` 不通过。
  后置两类（error / Post=false）都记 `StatusPostFailed`。
- **关键调用失败**：父帧立刻丢弃自己累积的 `plan`（置 nil，整体放弃，
  不是“只裁掉内层那段”），自身记 `StatusAborted` 并继续向上传播；
  顶层不提交，持久化状态零变化。
  测试：`TestCriticalFailureAbortsEverything`。
- **非关键调用（`Critical=false`）失败**：该子帧改记
  `StatusNonCriticalFailed`，其写入计划被丢弃，父帧继续后续子调用与自身
  Effect；失败子调用的名字**不会**进入输出表，后续流程没有任何渠道把它
  当成成功。测试：`TestNonCriticalFailureContinues`（三种失败来源各跑一遍）。

### 为什么“不能伪装成功”要靠声明期解决

仅在执行期让“取不到输出”变成零值会产生不可解释的结果。因此输出依赖必须
写进声明（`ChildSpec.Consumes`、`Action.EffectConsumes`），
`Registry.ValidateChain` 在链条确定时做两件事：

1. 依赖目标必须是声明顺序上**更早的兄弟子调用**（时序不可能向后依赖）；
2. 被依赖者必须是**关键调用**。后继若消费非关键调用输出，直接返回
   `declarationError`，引擎 `Run` 入口同样拦截且不产生任何帧
   （`TestDeclarationErrorConsumingNonCriticalOutput`）。

放弃的替代方案：让 Bind 在非关键失败时收到一个 “missing output” 哨兵自行
处理。放弃理由：把“失败可继续”与“输出可消费”这两个正交约束混进业务函数，
无法在链条确定时保证安全，且每个动作都要重复处理哨兵。

## 4. 自我触发检测

- 判定键 = `FrameKey{Action, CanonicalInput(Params)}`；输入规范化为
  键排序、值用 `%#v` 稳定序列化的字符串，`Params` 内容相同即同键。
  因此“同一动作 + 同一组输入”被直接或间接再次触发都能识别；
  动作相同但输入不同（如递归深度递减）正常放行。
- **检测在触发前、优先于前置评估**：`runFrame` 第一件事就是在当前调用栈
  `x.stack` 上线性查找同键，命中则记 `StatusSelfTriggerRejected` 并立即返回，
  不执行 Pre/Effect/Post，也不产生写入计划（`TestSelfTrigger...`
  断言被拒绝帧 `PreBasis == ""` 且 `WritePlan` 为空）。
- **拒绝不改变既有计划**：被拒绝帧只追加审计记录，不触碰父帧 `plan`；
  拒绝按关键性走正常传播——关键拒绝使外层放弃，非关键拒绝记
  `StatusNonCriticalFailed` 后外层继续。“链条其余部分以拒绝前状态向外传播”
  由不可变叠加视图 + 不回写父帧计划共同保证。

### 复杂度证明（不随历史链条总数增长）

检测只遍历 `x.stack`，长度恰为**当前链条已经发生的嵌套深度 d**：

- 命中时比较次数 ≤ d；未命中时比较次数恰为 d（栈底帧比较 1 次，
  深度 d 的帧比较 d 次）。
- `x.stack` 是每个顶层链条执行器私有的切片，不包含、也不查询任何
  系统历史链条；不存在全局调用表，因此历史链条总数增长带来的开销为 0。

可验证方式：`TestSelfTriggerCheckIsLinearInCurrentDepthOnly` 对纯判定函数
`SelfTriggerAt(stack, next)` 直接断言 `comparisons == depth`，
并构造 10000 个“历史帧键”证明它们不被接触；同键必检出、异键必放行，
深度 0..100 全枚举。

放弃的替代方案：全局去重表（action+input → 是否出现过）。放弃理由：
（a）必须按链条隔离否则误杀合法的跨链条重复调用；（b）全局表需要随链条
总数膨胀并加锁，直接违背“开销只与当前深度有关”。

## 5. 并发链条的可串行化

要求：多条并发独立链条在共享对象上的最终效果，等价于某个全序下逐条串行
执行的结果，且不破坏共享不变量。

采用**提交点串行化 + 乐观重放（OCC）**：

1. 每条链条在当前持久化快照上**无锁**纯函数计算（所有状态来自入口快照与
   叠加视图，函数不改存储，可安全并发）；
2. 提交进入全局临界区 `commitMu`：检查版本号 `version` 是否与读取时一致；
   若已有链条提交，则**整条放弃本次计算结果**，基于最新快照重新执行
   （Pre、自我触发检测、Effect、Post 全部重算，有界 `maxReplays` 次）；
3. 重放后链条看到的正是“提交全序中排在它前面的链条已全部生效”的串行状态，
   因此其前置判定与最终写入与按该全序朴素串行执行逐字节一致；
4. 提交前先在**假想提交后状态**上评估全局 `Invariant`，失败则整体拒绝
   （`InvariantError`，状态不变，见 `TestInvariantRejectsCommittingChain`）；
   通过才 `Commit` 并推进版本、记录链条 ID 到全序。

等价性不是口号：`RunNaiveSerial` 是一个独立的朴素串行参考实现，
`TestConcurrentChainsEquivalentToNaiveSerial` 与
`TestNaiveReplayMatchesEngineCommittedSet` 用 40/5 条并发链条在同一
`counter` 对象上扣减，分别从“总量守恒（终值=初值−min(总需求,初值)）”
和“按引擎实际提交全序逐链条串行重放，终值逐字段相等”两个角度对照。

放弃的替代方案：
- **两阶段锁 / 全程串行**：正确但把计算也串住，高并发下退化为单线程；
  OCC 只在提交点串行，冲突才重放，无冲突链条完全并行。
- **最后写入获胜（LWW）合并各链条对象补丁**：各链条内部自洽，但合并后
  可能得到任何串行顺序都达不到的状态（本题明确禁止），且无法表达
  “前置依赖读结果”的条件写入。

## 6. 对外暴露的四类结论（稳定可区分）

`Status` 枚举中四类分别为 `StatusPreFailed`、`StatusPostFailed`、
`StatusNonCriticalFailed`、`StatusSelfTriggerRejected`（另有成功
`Completed`、连带放弃 `Aborted`、声明错误 `DeclarationError` 供审计）。

- 自我触发检测在任何触发发生前完成，**优先级固定高于前置评估**。
- 其余三类相对优先级：一帧内按 自我触发 → Pre → 子调用（按声明顺序）
  → Effect → Post 推进；同输入走同一路径，分类结果稳定
  （`TestFourStatusClassesAreDistinctAndExposed` 四类各构造一次）。

## 7. 审计

每次顶层执行产出 `ChainRecord`：链条路径（`Path()`，含被拒绝的触发帧）、
每帧的深度、规范化输入、Pre/Effect/Post 判定依据（basis 字符串）、
该帧写入计划、输出、最终 `Status` 与失败原因、是否提交及提交全序序号。
核对样例见 `TestAuditTrailContents`。

## 8. 关键取舍汇总

| 议题 | 选择 | 放弃的方案 |
| --- | --- | --- |
| 子调用如何发起 | 静态 `Children` 声明 | 业务函数内动态触发（无法声明期拦截坏依赖） |
| 非关键输出依赖 | 声明期直接拒绝 | 执行期 missing 哨兵（不可解释、易漏处理） |
| 嵌套前置可见性 | 持久化 + 外层未提交计划的不可变叠加视图 | 提前提交外层写入（失败无法干净整体回滚） |
| 关键失败范围 | 父帧计划整体放弃 | 只丢弃内层、保留外层（职责边界不清） |
| 自我触发检测 | 每链私有调用栈，O(当前深度) | 全局去重表（跨链误杀、随历史膨胀） |
| 并发一致性 | 提交点串行化 + 乐观重放 | 2PL 全程串行；LWW 补丁合并 |
| 不变量 | 提交前在假想后状态上评估 | 事后修复（已经对外可见破坏） |

## 9. 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache GOPATH=/tmp/gopath

go test -race -count=3 ./...          # 竞态检测 + 重复执行稳定性
go test -v ./chain/                   # 查看每个用例的路径与结论
go test -coverprofile=/tmp/cov.out ./... && go tool cover -func=/tmp/cov.out
go vet ./... && gofmt -l .
```

关键用例与需求条目的对应：

- 关键失败整体放弃 / 非关键失败继续：`chain/chain_test.go`
- 自我触发多深度触发与放行边界、O(深度) 证据：`chain/selftrigger_test.go`
- 四类结论区分：`chain/classification_test.go`
- 并发交织 vs 朴素串行、不变量：`chain/engine_concurrent_test.go`
- 审计路径与各层依据：`TestAuditTrailContents`
