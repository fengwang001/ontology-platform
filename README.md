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

## 创意连续淘汰分流器（`ontology` 包）

`ontology/funnel.go` 实现带零点击护栏与中途加入的创意连续淘汰分流器。

### 构造

```go
f, err := ontology.NewFunnel(n, b, G) // n∈[2,64]，b∈[1,1e6]，G∈[1,1e9]
```

初始创意编号 `0..n-1`，初始均为活跃，累计曝光 `sT`、累计点击 `cT`、本轮曝光 `sR` 均为 0；轮次 `r` 从 1 起。

### 分流选择规则（`Next()`）

- 第 `r` 轮每创意配额 `q_r = b×2^min(r−1,20)`（逐轮翻倍，r≥21 后封顶在 ×2^20）。
- 在本轮 `sR < q_r` 的活跃创意中取 `sR` 最小者；`sR` 相同取编号小者。
- 选中创意的 `sT`、`sR` 各加一，然后在同一次 `Next()` 内依次完成：
  1. **护栏**：该创意 `cT==0 && sT>=G` 且活跃创意数 ≥2 时立即淘汰（原因 `guardrail`）；淘汰后仅剩一个活跃创意则立即结束、该创意胜出。
  2. **收轮**：若所有活跃创意的 `sR==q_r`，本轮结束。活跃创意按累计点击率 `cT/sT` 降序排列，比较一律使用整数交叉相乘 `cT_i×sT_j` 与 `cT_j×sT_i`（内部用 `math/big`，不丢精度），相等取编号小者；保留前 `ceil(活跃数/2)` 个，其余淘汰（原因 `round`）。只剩一个则结束，否则 `r++` 并清零所有活跃创意的 `sR`。
- 护栏淘汰可能正好淘汰掉最后一个未满配额的活跃创意；此时剩余活跃创意均已满额，同一次 `Next()` 内立即按收轮规则处理。

### 点击与中途加入

- `Click(id)`：已登记创意（含已淘汰者）在 `cT < sT` 时成功并 `cT++`；已淘汰/已结束创意的点击只记账，不影响任何淘汰结果。
- `Join(id)`：`id∈[0,1000]`、从未登记、已登记总数 <64 且未结束时，新创意以活跃状态加入当前轮（`sT=cT=sR=0`）。它须补满本轮配额，因此 `sR=0` 会被后续 `Next()` 优先选中。结束后不能 `Join`。

### 拒绝原因（严格按此优先级，只报第一个）

1. `ErrInvalidArgument`：构造参数越界、`Join` 编号越界；
2. `ErrFinished`：结束后调用 `Next`/`Join`；
3. `ErrUnknownArm`：`Click` 编号未登记；
4. `ErrArmExists`：`Join` 编号已登记（含已淘汰者）；
5. `ErrCapacity`：创意总数已达 64；
6. `ErrNoExposure`：`Click` 时 `cT >= sT`。

被拒绝的操作不改变任何创意、轮次与状态。所有方法以互斥锁串行化，护栏淘汰与收轮是 `Next` 不可分割的一部分，结果等价于某个串行顺序；相同操作序列重放得到完全相同的分流、淘汰事件与胜出者。

### 本地验证

```bash
go test ./ontology/ -v                      # 全部场景用例 + 2000 组随机对照
go test ./ontology/ -run TestRandomDifferentialAgainstNaive -v  # 仅随机对照
go test -race ./...                         # 竞态检测
go vet ./...
```

测试包含：`sR` 并列取编号小者、护栏恰在 `sT==G` 触发而 `G-1` 不触发、护栏淘汰最后一个未满配额者后立即收轮、护栏后仅剩一个即结束、奇数活跃数保留 `ceil(a/2)`、等点击率（含不同分母交叉相乘相等）按编号、中途加入的分母不同交叉相乘比较、已淘汰创意迟到点击不影响结果、点击先于曝光被拒、配额翻倍与封顶、结束后 `Next`/`Join` 被拒、64 满员后新编号报 `ErrCapacity` 而已登记编号报 `ErrArmExists`、各类拒绝优先级与不改状态，以及 2000 组随机操作序列与独立朴素模拟（`diff_test.go`）逐步对照；`-v` 日志打印每组输入参数、每步输入/输出/判定依据与终态 MATCH 摘要。
