# AUDIT — 不变量核对与实测数据

## 不变量 1：离开即完整恢复

- 保证位置：遮蔽靠父链查找实现，内层 `Declare` 只写本层 map（`scope/scope.go`
  `Declare`）；`table.Leave` 仅把 `cur` 指针移向父层（`table/table.go` `Leave`），
  从不修改外层作用域的任何字段。
- 钉住测试：`TestLeaveRestoresOuter`（`table/table_test.go`）。

## 不变量 2：解析唯一且可归属

- 保证位置：`resolve.Resolve` 沿链逐层哈希定位，本层命中即返回
  `Result{Decl, Depth}`（`resolve/resolve.go`）；同层重名被
  `scope.ErrDuplicateDeclare` 拒绝，不存在"多个同名声明"可选。
- 钉住测试：`TestRefOutcomes`（`table/table_test.go`）、`TestDuplicateDeclare`。

## 不变量 3：前向引用只在允许的种类上成立

- 保证位置：`resolve.Resolve`：`KindForward` 命中即成功；`KindStrict` 且
  `ref.Pos < decl.Pos` 返回 `ErrUseBeforeDeclare`；整条链无该名字返回
  `ErrUndefined`。三者为互不相同的哨兵错误。
- 钉住测试：`TestResolveOutcomes`（`resolve/resolve_test.go`）、`TestErrorsDistinct`。

## 不变量 4：捕获集合精确

- 保证位置：`capture.Of` 只收集 `ref.Scope == 当前层` 且解析深度严格更浅的命中，
  按（深度, 名字）去重；本层有声明的名字本地裁决，绝不进入捕获集合。
- 钉住测试：`TestCapturesExact`（`table/table_test.go`）。

## 不变量 5：失败不留痕

- 保证位置：所有拒绝路径在修改任何状态之前返回（`table.go` 的 `Enter`/`Leave`/
  `Declare` 前置检查，`scope.go` 的重复检查）；`Ref` 失败不写入引用日志。
- 钉住测试：`TestRejectedOpsNoTrace`（`table/table_test.go`）。

## 查看条目实测（深度 1000，每层 50 条不相干声明）

| 档位 | 实测 looked | 断言上界 |
| --- | --- | --- |
| 目标在最外层 | 1001 | 深度+1 = 1001 |
| 目标在最内层 | 1 | 4（小常数） |

- 每层一次哈希定位计 1，不遍历层内声明表。
- 钉住测试：`TestLookupComplexity`（`resolve/resolve_test.go`）。

## 并发

- `Ref`/`Captures`/`SelfCheck` 走 `RWMutex` 读锁，引用日志由独立互斥锁保护，
  计数器为 `atomic.Int64`。
- 钉住测试：`TestConcurrentRef`（`table/concurrency_test.go`），`go test -race` 干净。
