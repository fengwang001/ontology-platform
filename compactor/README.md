# compactor — Universal 风格压实选择器

`compactor` 在「按新到旧排列」的有序运行集合上，按固定次序套用四条规则
挑选压实计划，并随计划的完成（Done）与中止（Abort）演化运行集合。
所有方法由一把互斥锁保护，可并发调用；等价于某个串行交错，相同操作序列
重放得到完全相同的计划编号、运行编号与集合状态。

## 配置与构造

`New(Config)`，任一条件不满足返回 `ErrParam`：

- `2 ≤ MinRuns ≤ MaxRuns`
- `1 ≤ A ≤ 10^6`（空间放大百分比）
- `0 ≤ Rho ≤ 10^4`（大小比例百分比，0 表示只能并入不大于累计和的运行）
- `2 ≤ MinMerge ≤ MaxMerge`
- `Cmax ≥ 1`（未结束计划上限）
- `P ≥ 0`（周期；0 关闭 Periodic 规则）

运行 `Run{ID, Size, Created, Busy, Fails}`：`Size ∈ [1, 10^12]`，
`Fails` 初值 0。运行编号与 `Done` 产生的替代运行共用一个从 1 递增的
计数器；计划编号同样从 1 递增、独立计数。

## 全局时钟

- 时钟从 0 起、只减不增判定基准为「已接受过的最大 `now`」。
- `AddRun`、`Pick`、`Done` 成功后把时钟推进到各自的 `now`（相等允许）；
  `Abort` 不带时钟，不读取也不推进时钟。
- `now < 0` 属 `ErrParam`。

## Pick(now) 的四条规则（严格按序，取第一个成立者）

运行集合下标 0 为最新端，`n` 为运行总数。`n < MinRuns` 时前三条规则
一律不考虑，只剩 Periodic 可能命中。

1. **SpaceAmp（空间放大）** — `n ≥ MinRuns` 且**没有任何忙运行**。
   设最旧运行大小为 `S`、其余运行大小之和为 `E`，
   `E*100 ≥ A*S`（**恰等成立**）则选中全部运行。
2. **SizeRatio（大小比例）** — `n ≥ MinRuns`。从最新端依次取起点：
   - 忙运行与 `fails ≥ 2` 的运行既不能作起点，也不能被延伸并入
     （延伸时遇到即停止）。
   - 以 `acc = size(i)`、个数 1 向旧端延伸；当下一运行不忙、
     `fails < 2`、当前个数 `< MaxMerge` 且
     `size*100 ≤ acc*(100+Rho)`（**恰等延伸**，用**累计和 `acc`**
     而非上一个运行）时并入，并把其大小累加进 `acc`。
   - 停止时个数 `≥ MinMerge` 即选中该段；否则换下一个起点，选第一个成功者。
3. **CountReduce（个数削减）** — `n > MaxRuns`。
   `c = min(n-MaxRuns+1, MaxMerge)`，从最新端起找第一个
   **连续 c 个都不忙**的窗口（忙运行会把窗口隔断）并选中。
4. **Periodic（周期重写）** — `P > 0` 时，取**位置最旧**、不忙且
   `now - created ≥ P`（**恰等成立**）的单个运行，计划只含这一个运行。

四条规则都不成立时返回 `ErrNotNeeded`。比较一律用 `big.Int` 完成
（`E*100` 与 `A*S`、`size*100` 与 `acc*(100+Rho)`），杜绝 64 位溢出。

## 忙标记与计划生命周期

- `Pick` 成功：选中的运行**全部置忙**，生成 `Plan{ID, Reason, Runs, Total}`，
  `Runs` 按新到旧排列，`Total` 为大小之和；占用一个活动计划槽位。
- `Done(now, planID, out)`：把该计划的运行**整体替换**为一个新运行——
  大小 `out ∈ [1,10^12]`，编号取「下一个运行编号」，`created = now`，
  不忙，`fails = 0`，位置就是被替换段的位置；计划随即结束。
  计划按运行**编号**跟踪，即使 Pick 之后又在最新端 `AddRun`，
  也能正确定位原段。
- `Abort(planID)`：取消计划，解除这些运行的忙标记并让其 `fails` 各加 1，
  集合其余不变。`fails` 只影响 SizeRatio；SpaceAmp、CountReduce、
  Periodic 都不看它。
- 对不存在或已结束的计划调用 `Done`/`Abort` 返回 `ErrUnknown`。

## 错误与先后次序

被拒绝的操作不改变任何状态、不推进时钟、不消耗运行/计划编号。判定次序：

1. 参数非法 → `ErrParam`（含非法 `now`、`size`、`out`、`planID < 1`）
2. 时钟倒退 → `ErrClock`（`now` 小于已接受的最大 `now`；Abort 无此步）
3. 活动计划已满 → `ErrBusy`（Pick 专用；先于规则评估，即先于 `ErrNotNeeded`）
4. 计划不存在或已结束 → `ErrUnknown`（Done/Abort）
5. 无规则成立 → `ErrNotNeeded`（Pick）

## 可复现性与 probes

- 所有非确定性来自操作调用次序；锁使并发结果等价于某个串行顺序。
- `Probes()` 返回 SizeRatio 的累计考察次数：每取一个起点计 1，
  每检查一个待并入的下一个运行计 1；单次 Pick 的增量不超过
  `n*MaxMerge`（测试中对每次 Pick 断言上界）。
- 差分测试 `TestNaiveDifferential` 用 2000 组随机操作序列，逐条对照
  一份独立照规格抄写的朴素实现；`-v` 日志打印每步输入、两侧输出与
  命中规则的判定依据（含 `E/S`、`acc/cnt/probes`、`c` 与窗口、
  `age/P`），任何分歧都附带完整日志便于复现。

## 本地验证

```bash
# 全量测试（go.mod 要求 Go 1.26+）
go test ./...

# 竞态检测 + 全部用例（含 2000 组差分与高并发压力）
go test -race -count=1 ./...

# 查看差分日志（输入/输出/判定依据）
go test -v -run TestNaiveDifferential ./compactor

# 仅四条规则的边界用例
go test -v -run 'TestSpaceAmp|TestSizeRatio|TestCountReduce|TestPeriodic' ./compactor

gofmt -l .
go vet ./...
```
