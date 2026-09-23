# AUDIT — 不变量核对与实测数据

## 第二节五条不变量

1. **解必须真的满足全部约束**
   - 保证位置：`solve/validate.go` 的 `Validate` 独立逐条核验根需求与每个
     入选 `包@版本` 声明的全部约束；求解器自身的成员判定与 `rng.Contains`
     共用同一比较全序。
   - 钉住测试：`TestSolveScenarios`（每个可行用例都过 `Validate`）、
     `TestPruning`、`TestConcurrentSolve`（并发结果逐份 `Validate`）。
2. **确定性（逐位相同、与登记/声明顺序无关）**
   - 保证位置：`graph.Versions` 降序排序候选；`solve.Solve` 排序根需求；
     `search.next` 以「可行数最少 + 包名」打破平局；`explain` 按规范键排序
     约束并取字典序最小渲染；`Solution.String` 按包名排序输出。
   - 钉住测试：`TestDeterminism`（重复求解 + 打乱登记/声明顺序结果相同）、
     `TestConflictExplanation`（解释重复三次逐位相同）、`TestConcurrentSolve`。
3. **无解必须可解释（含约束链）**
   - 保证位置：`solve/explain.go`：`explain` 定位第一个使交为空的前缀并
     取不相容约束对，`chain` 回溯至根需求；`ConflictError.Chain` 承载文本。
   - 钉住测试：`TestConflictExplanation`（断言链含根需求、`app@1.0.0` 的
     约束、不相容对）、`TestCircularUnsatisfiable`（循环依赖无解仍给链）。
4. **已确定的选择不被悄悄改写**
   - 保证位置：`search.tryAssign` 的 `undo` 在回溯时删除赋值并截断被施加
     约束（空键一并删除）；解是 `map[包]版本`，结构上每包恰好一个版本；
     成功时整体拷贝返回。
   - 钉住测试：`TestSolveScenarios/backtrack_to_lower_version`（回溯后解中
     无残留）、`TestPruning`（大量回溯后每包仍恰好一个版本）。
5. **不相关的包不出现在解里**
   - 保证位置：`search.next` 只从「被根或已选版本施加过约束」的包中取
     候选；`undo` 删除空约束键；`Validate` 反向校验可达性。
   - 钉住测试：`TestSolveScenarios/unreachable_package_excluded`。

## 第四节搜索规模实测

- 场景：10 包 × 10 版本，唯一解为全 `0.0.0`（不在降序搜索的首条路径）。
- 实测尝试次数：**100**（每包 9 次失败候选 + 1 次成功），断言上限 10000，
  暴力穷举为 10^10。前向检查在每次赋值后立即验证受影响包的可行集，
  坏分支一次尝试即剪除。
- 钉住测试：`TestPruning`（`attempts=100 < 10000`）。
- 计数器：`Solver.attempts` 为非导出 `atomic.Int64` 字段，不出现在
  `Solve` 的签名或返回值中；只读访问器 `Attempts()` 供演示程序读数。

## 第五节故障注入

- 循环依赖可求解：`TestSolveScenarios/circular_dependency_solvable`；
  循环且无解：`TestCircularUnsatisfiable`。
- 四类可判定错误互不相同：`TestDecodableErrors`（`ver.ErrInvalidVersion`、
  `rng.ErrInvalidConstraint`、`graph.ErrDuplicateVersion`、
  `solve.ErrUnknownPackage` 两两不可互相 `errors.Is`）。
- 超预算 ≠ 无解：`TestBudgetVsNoSolution`（`ErrBudgetExceeded` 不匹配
  `ErrNoSolution`，同输入放宽预算后可解）。
- 重复登记不改变已有登记：`graph` 包 `TestAddVersion`（重复登记报错后
  版本数不变）。

## 第六节并发

- `TestConcurrentSolve`：32 个 goroutine 共享同一 `Solver`/`Graph` 求解
  同一根需求，结果逐位相同且逐份过 `Validate`；`go test -race` 干净。
  共享可变状态仅 `attempts` 计数器（atomic）。
