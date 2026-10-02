# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 压实选择器（`compactor.go`）

`ontology` 包提供 Universal 风格的压实选择器 `Compactor`，运行集合按**新到旧**排列。

### 配置与构造

`New(Config)` 校验下列条件，任一不满足返回 `ErrParam`：

- `2 ≤ MinRuns ≤ MaxRuns`
- `1 ≤ A ≤ 1_000_000`（空间放大百分比）
- `0 ≤ Rho ≤ 10_000`（大小比例百分比）
- `2 ≤ MinMerge ≤ MaxMerge`
- `Cmax ≥ 1`，`P ≥ 0`（`P=0` 关闭周期规则）

### 四条规则的次序与取等

`Pick(now)` 按以下顺序取**第一个成立**的规则，所有比较均使用整数乘法避免浮点误差：

1. **SpaceAmp**：`n ≥ MinRuns` 且没有任何运行为忙；设最旧运行大小为 `S`、其余大小之和为 `E`，当 `E*100 ≥ A*S`（**恰等成立**）时选中全部运行。任一运行为忙即让位给后续规则。
2. **SizeRatio**：`n ≥ MinRuns`；从最新端依次取起点 `i`，忙或 `fails ≥ 2` 的运行既不能作起点，延伸时遇到也立即停止。以 `acc=size(i)`、个数 1 向旧端延伸，当下一个运行不忙、`fails<2`、个数 `< MaxMerge` 且 `size*100 ≤ acc*(100+Rho)`（**恰等延伸**）时并入并累加 `acc`；停止后个数 `≥ MinMerge` 即选中该段，否则换下一个起点，取第一个成功的起点。比较对象是**段内累计和 `acc`**，不是前一个运行。
3. **CountReduce**：`n > MaxRuns`；`c = min(n−MaxRuns+1, MaxMerge)`，从最新端起找第一个连续 `c` 个全部不忙的窗口。
4. **Periodic**：`P > 0`；取位置最旧的、不忙且 `now−created ≥ P`（**恰等成立**）的单个运行。

`n < MinRuns` 时前三条均不考虑，只可能走到 Periodic。四条都不成立返回 `ErrNotNeeded`。

### 忙标记与计划生命周期

- `Pick` 成功返回 `Plan{ID, Reason, Runs, Total}`（`Runs` 新到旧，`Total` 为大小之和），计划编号从 1 递增，被选运行整体置忙；未结束计划数达到 `Cmax` 时返回 `ErrBusy`。
- `Done(now, planID, out)`：把该计划覆盖的连续段整体替换为一个新运行，大小 `out`、`created=now`、不忙、`fails=0`，位置就是被替换段的位置；运行编号取共享计数器的下一个值（与 `AddRun` 共用）。
- `Abort(planID)`：不带时钟；清除该计划运行的忙标记并将各自 `fails++`。`fails` 只影响 SizeRatio，SpaceAmp、CountReduce、Periodic 均不读取它；Done 产生的新运行 `fails=0`。
- 计划结束（Done 或 Abort）后再引用同一 `planID` 返回 `ErrUnknown`。

### 时钟与错误次序

全局时钟起点 0，只接受不减的 `now`；成功的 `AddRun`/`Pick`/`Done` 会把已接受的最大时钟推进到各自的 `now`，`Abort` 不推进时钟。错误判定先后次序为：

1. `ErrParam`（非法参数，含负时钟、越界大小）
2. `ErrClock`（`now` 小于已接受的最大时钟）
3. `ErrBusy`（未结束计划已达 `Cmax`，先于 `ErrNotNeeded`）
4. `ErrUnknown`（计划不存在或已结束）
5. `ErrNotNeeded`（四条规则都不成立）

被拒绝的操作不改变任何状态、不推进时钟、不消耗运行或计划编号。全部方法用互斥保护，可并发调用，等价于某个串行顺序；相同操作序列重放得到完全一致的计划与运行集合。

### 本地验证

```bash
# 全部用例（含 2000 组随机操作序列与朴素模拟器对照、竞态检测）
go test -race -v ./...

# 只跑 2000 组随机对照（失败时日志含输入、输出、每步判定依据）
go test -race -run TestRandomDifferential2000 -v ./ontology

# 快速跳过随机对照
go test -short ./...
```

随机对照测试（`fuzz_test.go`、`fuzz_types_test.go`、`sim_test.go`）内置一个按上述规则逐行实现的朴素模拟器，逐步比对错误、计划编号/原因/范围/大小、忙标记、`fails` 与每步运行集合，并校验 SizeRatio 的 `probes` 计数不超过 `n*MaxMerge`。
