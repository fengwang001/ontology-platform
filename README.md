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

## A/B 序贯检验停止判定器

实现在 `sequential_stopping.go`，入口为 `NewSequentialABStopper` 和 `SequentialABStopper.Look`。构造参数包括分流比 `rA:rB`、最小组样本 `nmin`、有效检视最小增量 `minStep`、样本上限 `Nmax`、显著边界表 `T_1...T_k`、无效边界 `Tf` 与比例容差 `tau`。

### 拒绝优先级

每次 `Look(nA,cA,nB,cB)` 先按以下顺序检查，拒绝时不改变任何状态、有效检视次数、上次计数 n 或上次被接受的检视：

1. 参数非法：构造参数越界，或调用不满足 `0 <= cA <= nA <= 10^6`、`0 <= cB <= nB <= 10^6`。
2. 已停止：当前状态为 `Stopped`。
3. 数据回退：四个累计值中任一个小于上一次被接受的检视。

对应错误分别为 `ErrInvalidArguments`、`ErrStopped`、`ErrDataRegression`。

### 接受后的判定次序

令 `n=nA+nB`、`c=cA+cB`、`D=cB*nA-cA*nB`。被接受的检视按下面顺序判定：

1. 仅当 `n >= 2*nmin` 时检查分流比例：
   `|nA*rB-nB*rA|*100 > tau*(nA*rB+nB*rA)` 命中则 `RatioInvalid` 并停止；该检视不计数。
2. `nA < nmin` 或 `nB < nmin` 时返回 `ContinueSampleTooSmall`，不计数。
3. `n-上次计数n < minStep` 时返回 `ContinueObservation`，不计数；上次计数 n 初值为 0。
4. 否则有效检视次数 `i` 加一，更新上次计数 n，并选择边界 `T_min(i,k)`；`i>k` 后持续沿用 `T_k`。

每次被接受的检视，无论是否有效、无论是否停止，都会更新“上一次被接受的检视”。

### 整数化统计量

原始统计量为：

```text
z² = D²*n / (nA*nB*c*(n-c))
```

由于边界表数值表示 `100*z²` 的下限，判定时全程使用整数：

```text
左侧 = D²*n*100
右侧 = T*nA*nB*c*(n-c)
```

`左侧 >= 右侧` 时显著：`D>0` 为 `Winner`，`D<0` 为 `Worse`，`D=0` 只可能在边界为 0 时出现；本实现按构造要求边界均为正数。否则按顺序判定：

1. `n >= Nmax`：`Futile` 并停止。
2. `2*n >= Nmax` 且 `D²*n*100 < Tf*nA*nB*c*(n-c)`：`Futile` 并停止。
3. 其他情况为 `ContinueRunning`。

当 `c=0` 或 `c=n` 时统计量未定义，按 `z²=0` 处理：显著判定必为否；无效判定中“小于”当且仅当 `Tf>0` 时成立。该分支不通过两侧乘积直接比较，避免把零乘积误判为小于。

乘积可能约为 `2*10^32`，实现使用 `math/big.Int`；比例检查及构造范围仍使用可容纳给定范围的 64 位整数。

### 状态与并发

`Status()` 返回 `StatusSnapshot`，包含运行状态、有效检视次数、上次计数 n 与最近一次结论。`Look` 与 `Status` 由互斥保护，并发执行等价于某个串行顺序；进入停止状态后结论不再改变。

### 本地验证

本环境 Go 位于 `/usr/local/go/bin/go`，默认构建缓存不可写时可显式指定：

```bash
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test ./...
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -race -v ./...
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go vet ./...
```

随机对照测试为 2000 组固定种子序列，测试内朴素模拟器独立按相同规则维护状态并使用大整数。使用 `-v` 可查看每条输入、输出、D、边界、左右统计量、拒绝错误和命中原因：

```bash
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -run TestRandomSequencesAgainstNaiveSimulation -v ./...
```
