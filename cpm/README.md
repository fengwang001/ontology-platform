# cpm — 增量关键路径与时差维护器

`cpm` 包在任务网络上随任务与依赖的增删改，持续维护最早/最晚时间、总时差、
自由时差与关键任务集合，并对每次被接受的更新报告精确的变化。所有更新与
查询均可并发调用（内部互斥锁串行化，结果等价于某个串行顺序，查询不会看到
只完成一半的更新）；相同的更新序列重放得到完全相同的报告与关键路径。

## 数值定义

对每个任务 `v`（工期 `dur(v)`、最早开始约束 `snet(v)`、最晚完成约束
`fnlt(v)`，`-1` 表示无）与依赖 `(u,v,lag)`（`lag` 可负，负值为提前搭接）：

- `ES(v) = max(snet(v), max_{(u,v,lag)} EF(u)+lag)`，`EF(v) = ES(v)+dur(v)`
- `PF = max_v EF(v)`（无任务时为 0）
- `LF(v) = min(D, fnlt(v)（若有）, min_{(v,w,lag)} LS(w)-lag)`，`LS(v) = LF(v)-dur(v)`
- 总时差 `TF(v) = LF(v)-EF(v)`（可为负）
- 自由时差 `FF(v) = min_{(v,w,lag)} (ES(w)-lag-EF(v))`；无后继时 `FF(v) = PF-EF(v)`

任意时刻 `ES` 不小于 `snet` 与所有前驱 `EF+lag`、`LF` 不大于 `D`/`fnlt` 与所有
后继 `LS-lag`，且二者都取到等号之一（即取最大/最小而非任一合法值）。

## 驱动边、关键任务与关键路径

- 依赖 `(u,v,lag)` 是**驱动边**当且仅当 `ES(v) == EF(u)+lag`。
- **关键任务**是 `TF` 等于全部任务 `TF` 最小值的任务；有任务时必非空，
  期限宽松时关键任务的 `TF` 也可大于 0，期限紧张时全为负。
- `CriticalPath()`：在关键任务中取没有"来自关键任务的驱动边"的编号最小者
  作起点，之后每步取当前任务的、指向关键任务的驱动边终点中编号最小者，
  直到没有为止；无任务时为空。

## 变更报告口径

每次被接受的更新（`AddTask`/`AddDep`/`RemoveDep`/`SetDuration`/
`SetConstraint`）返回 `Report`：

- `ChangedES` / `ChangedLF`：ES、LF 发生变化的既有任务编号（升序；
  新任务的初始化不算变化）。
- `OldPF` / `NewPF`：更新前后的项目完成时间。
- `CritAdded` / `CritRemoved`：进入与退出关键任务集合的编号（升序）。
  关键集合可能因最小 TF 变动而整体切换，即使 ES 与 LF 都没变。

被拒绝的操作返回可区分的原因（`ErrInvalidParam`、`ErrTaskNotFound`、
`ErrDepExists`、`ErrTaskLimit`、`ErrDepLimit`、`ErrDepNotFound`、`ErrCycle`、
`ErrNoBaseline`），且不改变任何任务、依赖、基线与计数器。`AddDep` 按
参数非法、任务不存在、依赖已存在、依赖数已满、成环的顺序只报第一个。

## 基线

`SetBaseline()` 记录当前全部任务的 EF 作为基线（覆盖此前基线）；
`Variance(v)` 返回 `EF(v)` 减去基线 `EF(v)`；基线之后新建的任务或尚无基线时
返回 `ErrNoBaseline`。

## 增量性与复杂度

更新只重算受影响部分：内部按拓扑秩用堆做懒惰传播，每个任务在一次更新中
至多被重算一次，变化不再传播时立即停止。非导出计数器 `fwdEval`/`bwdEval`
度量一次更新重算 ES/LF 的次数，满足：设 X 为操作直接涉及的任务集合，则
`fwdEval ≤ |X| + Σ_{v∈X∪ChangedES} (1+出度(v))`，
`bwdEval ≤ |X| + Σ_{v∈X∪ChangedLF} (1+入度(v))`。

## 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素整网重算实现对拍、
# n=1000/100000 长链 fwdEval/bwdEval 有界性、并发测试）
go test ./cpm/

# 竞态检测
go test -race ./cpm/

# 查看随机对拍日志（输入、输出与判定依据）
go test ./cpm/ -run TestRandomDifferential -v

# 查看长链上的求值计数
go test ./cpm/ -run TestChainEvalBounded -v
```
