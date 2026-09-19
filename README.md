# ontology — Action 事务执行引擎

纯标准库实现的本体平台 Action 事务引擎：把一次业务动作对多个对象与关系的
成批修改作为一个不可分割的事务提交。状态只保存在进程内存，不依赖任何外部服务。

## 运行演示与测试

```bash
go run ./cmd/demo          # 一屏内跑完的端到端演示，退出码 0
go test -count=1 ./...     # 全部测试
go test -race -count=1 ./...
gofmt -l .                 # 无输出
go vet ./...
```

## 声明 ActionType

用 `NewActionType(name, schema, objectTypes, hooks, handler)` 声明：

- `Schema` 是参数契约：每个 `ParamSpec` 含 `Name`、`Type`
  （`string` / `int` / `bool` / `float`）、`Required` 与可选 `Default`。
- `objectTypes` 声明该 Action 允许触碰的对象类型；写入未声明类型会失败并回滚。

调用时参数按 schema 校验，一次返回**全部**问题（`ParamErrors`，即
`[]ParamError`），每一条的 `Kind` 可分别判定：

- `ParamMissingRequired` 缺失必填
- `ParamTypeMismatch` 类型不符
- `ParamUnknown` 传入未声明参数

可选参数缺失时填入默认值。默认值在声明时被深拷贝冻结成快照，
`map`/`slice` 等引用类型在多次调用之间互不污染。

## 前置钩子契约

`HookFunc` 按声明顺序执行，签名为
`func(tx *ontology.Txn, args map[string]any) (reason string)`：

- 返回非空 `reason` 表示拒绝；错误类型为 `*HookRejectError`，
  `Index` 是第几个钩子（从 1 开始），`Reason` 是拒绝理由。
- 钩子只能读：`tx.GetObject` / `tx.HasRelation` 能读到同一事务内
  （含外层 Action）已发生但尚未提交的修改。
- 钩子内任何写操作都不会生效；该越权会被记录，并在提交时使整个 Action
  失败，错误为 `*HookWriteError`（指出是哪个 Action 的第几个钩子）。
  即使嵌套场景中外层吞掉了内层错误，任意层级的越权仍会导致顶层整体失败。
- 拒绝与越权都不会提前中断钩子序列，所有钩子会继续跑完。

## 回滚与补偿

一次 Action 内可 `CreateObject`、`SetAttribute`、`AddRelation`、
`RemoveRelation`。每步都写入逆序撤销日志。执行中任意一步失败
（校验失败、约束冲突、钩子拒绝/越权、业务 handler 返回错误）时：

- 已发生的全部修改按日志**逆序撤销**，回到 Action 开始前的状态，
  不残留半成品对象或悬空关系。
- 若某个撤销动作本身失败，该步骤会被记录（`*RollbackError.FailedStep`），
  存储立即冻结：之后一切写入返回 `*InconsistentError`。
  查询接口为 `store.InconsistentStep()` 与 `store.IsConsistent()`。

## 嵌套 Action 与保存点

在 handler 内用 `tx.Call(name, args)` 调用另一个 Action：

- 内层失败默认只逆序回滚到内层自己的保存点，外层可选择继续或整体放弃。
- 外层最终失败时，即使内层已经“成功”，其修改也随外层一起回滚。
- 嵌套深度上限为 `MaxDepth`（16），超限返回 `*DepthError` 并给出完整调用链。
- 同一 Action 在调用链中重复出现（直接或间接递归）返回
  `*RecursionError`，`Chain` 给出完整调用链。

## 执行日志

每次**成功提交**产出一条不可变 `Record`：`Seq`（提交序号）、`Action`、
规范化后的 `Params`、`Impacts`（对象创建/属性修改/关系增删清单）。

- 序号从 1 开始，严格递增、无空洞、并发下不重号（引擎在存储锁内分配）。
- 失败的 Action 不产生记录、不占用序号。
- 查询：`eng.Log().Range(from, to)` 返回序号闭区间内的记录副本，
  稳定按序号升序；`to <= 0` 表示取到最新；`eng.Log().LastSeq()` 取最新序号。
