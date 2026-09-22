# 审计：五条不变量的代码保证与测试钉点

## 不变量 1：解真的满足全部约束
- 保证位置：`solve/solve.go` 的 `Validate` 逐条核验根约束、每个被选版本的
  每条依赖约束（`d.Range.Contains`）；并校验解的可达闭包与选择集合一致。
- 求解路径保证：`solve/search.go` 的 `candidates`（`solve/filter.go`）只保留
  满足域内全部条目的候选，`guess` 后重新传播依赖条目。
- 测试：`TestSolveBasicAndDeterminism`（求解后必须过 Validate）、
  `TestCyclesAreSolvable`、`TestConcurrentSolveIdentical`（并发内也调 Validate）。

## 不变量 2：确定性（与登记/声明顺序无关）
- 保证位置：候选版本登记即升序（`graph/graph.go` `insertSorted`），
  依赖条目按 `(Target, Range)` 排序（`sortEntries`），
  搜索按包名升序+MRV、版本高到低尝试（`solve/search.go` `run`/`pickPkg`），
  根需求按包名排序（`solve.solve`）。
- 测试：`TestSolveBasicAndDeterminism`（含打乱登记/声明顺序两份图逐位对比）、
  `TestDepOrderIndependent`、`TestConcurrentSolveIdentical`。

## 不变量 3：无解给出冲突约束链
- 保证位置：`solve/conflict.go` 的 `ConflictError`（`Unwrap→ErrNoSolution`）、
  `chainFor` 沿 `rng.Origin` 回溯到 `<root>`；互斥对由 `firstRejection`/
  `incompatiblePairs` 按 `originLess` 全序确定（`solve/filter.go`）。
- 测试：`TestUnsatisfiableConflictChain`（断言含 `<root>`、`a@1.0.0`、
  两条互斥约束原文，且重复求解错误信息逐位相同）。

## 不变量 4：回溯不留下痕迹；每包恰好一个版本
- 保证位置：`solve/search.go` `undoGuess` 删除 `chosen`、按来源精确移除
  本次加入的条目，随后 `reachable` 重算活跃集；`chosen` 为
  `map[string]ver.Version`，结构上每包至多一个版本。
- 测试：`TestSolveBasicAndDeterminism`（最终解过 Validate 且无重复）、
  `TestPruningFarBelowBruteForce`（深度回溯后仍唯一正确解）。

## 不变量 5：不可达包不出现在解里
- 保证位置：活跃集由根条目+被选版本依赖按需扩展；`undoGuess` 后 `reachable`
  清除不可达包；`Validate` 对解中每个包复核根可达闭包。
- 测试：`TestSolveBasicAndDeterminism` 中登记 `unrelated` 并断言其不在解中。

## 第四节：剪枝实测
- 计数器：`Solution.attempts`（非导出字段，仅经只读方法 `Attempts()` 读值），
  每次「包@版本」尝试在 `search.tick` 自增。
- 测试：`TestPruningFarBelowBruteForce`（10 包×10 版本，唯一解 p0=10.0.0，
  朴素最坏 10^10）。实测 **attempts=10**（断言上限 10000）。
  原因：依赖随高版本选择立即向下传播 `>=k` 约束，域被单调剪窄，
  一次路径即得解；MRV 选包也阻止跨包组合爆炸。

## 资源上限与可判定错误（第五节）核对
- 循环依赖可解：`TestCyclesAreSolvable`（结构上靠 `reachable` 访问标记终止）。
- 未登记包：`*UnknownPackageError`（`errors.Is → solve.ErrUnknownPackage/
  graph.ErrUnknownPackage`），见 `TestDistinctErrors/unknown-*`。
- 非法约束：`rng.ErrInvalidConstraint`，见 `rng.TestParseInvalid`、
  `TestDistinctErrors/bad-root-constraint`。
- 非法版本：`ver.ErrInvalidVersion`，见 `ver.TestParseInvalid`、
  `TestDistinctErrors/bad-version-in-data`。
- 搜索预算：`ErrSearchBudget` 与 `ErrNoSolution` 分别被
  `errors.Is` 区分，见 `TestBudgetDistinctFromNoSolution`、`TestBudgetTripped`。
- 重复版本不改原登记：`graph.TestDuplicateDoesNotMutate`。

## 并发（第六节）
- 求解只在调用栈内构造私有 `search` 状态，图只读；无互斥即安全。
- 测试：`TestConcurrentSolveIdentical`（32 goroutine，无 sleep，结果逐位相同），
  `go test -race -count=1 ./...` 干净。
