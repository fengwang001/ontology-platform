# AUDIT.md — 不变量核验

## 不变量 1：离开即完整恢复
- 保证：声明只存于所属 `Scope` 的 map（scope/scope.go），遮蔽不触碰
  外层；`Table.Leave` 仅弹出栈顶与该层引用记录（table/table.go），
  外层对象零写入。
- 测试：`TestLeaveRestores`（table/table_test.go）。

## 不变量 2：解析唯一且可归属
- 保证：同层名字是 map 键（结构唯一）且 `Scope.Declare` 拒绝重复；
  `Resolver.Resolve` 返回 (Decl, Depth) 二元组或哨兵错误
  （resolve/resolve.go）。
- 测试：`TestResolveSemantics`、`TestDuplicateDeclare`。

## 不变量 3：前向引用按种类判定，三类错误互异
- 保证：`Resolve` 中 pos 早于声明位置时 Forward 放行、Plain 报
  `ErrUseBeforeDecl`；全链查无此名报 `ErrUndefined`；二者与
  `ErrEmptyName` 互不相同（errors.Is 两两为假）。
- 测试：`TestResolveSemantics`、`TestErrorsDistinct`。

## 不变量 4：捕获集合精确
- 保证：`Table` 按层记录引用，`capture.Outer` 只保留命中深度严格
  小于本层深度的条目，并按 (Decl, Depth) 去重（capture/capture.go）。
- 测试：`TestCapturesExact`。

## 不变量 5：失败不留痕
- 保证：`Enter`/`Declare`/`Leave` 全部先校验后修改，任何拒绝路径
  零写入（table/table.go）；拒绝后表可继续使用，不进入终态。
- 测试：`TestRejectedOpsKeepState`。

## 复杂度实测（第四节两档，计数器为 Resolver 非导出字段）
- 深度 1000、每层 50 条无关声明、目标在最外层：实测查看条目数
  = 1000（上界：引用点深度 999 + 常数 1）。
- 深度 1000、目标在最内层：实测查看条目数 = 1（上界：常数 1）。
- 每层恰好一次 map 哈希定位，不遍历该层声明表。
- 测试：`TestLookupCountBound`（resolve/resolve_test.go，白盒）。

## 并发
- `Ref`/`Captures`/`SelfCheck` 经 `Table` 互斥锁串行化共享状态，
  计数器为 atomic；N 个 goroutine 解析结果逐位相同。
- 测试：`TestConcurrentRefs`（table/race_test.go），`go test -race` 干净。
