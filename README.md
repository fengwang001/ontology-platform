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

## 灰度分批发布评审器

实现位于 `ontology/rollout.go`，所有操作由互斥锁串行化，可从多个 goroutine 并发调用。

### 分批累计与去重

输入严格递增的累计百分比 `p_0 < p_1 < … < p_m-1 = 100`，每批实例数为：

```text
cum_k = ceil(N * p_k / 100)
```

若计算出的累计实例数与前一批相同，则删除后出现的批次，保留先出现的批次。因为最后一个百分比必须是 `100`，去重后最后一批的累计实例数必定恰为 `N`。

### 检视与闸门

`Start(now)` 只能在未开始状态调用，进入发布中并以第 0 批开始，`st = now`。每个发布批次分别累计新版本和基线版本的请求数、错误数：`(rn, en, rb, eb)`。

`Observe(now, drn, den, drb, deb)` 先校验并累加增量，然后：

- `now < st + S`：返回 `soaking`，只累加，不算一次检视。
- `rn < Nmin`：返回 `insufficient_sample`，不改变 `ps`、`fs`。
- 否则执行闸门，且只有严格大于才失败：

```text
en >= Ef
and en * rb * 100 > eb * rn * (100 + tol)
```

乘积使用 `math/big.Int` 精确比较，避免 64 位溢出；两边相等判通过。`rb = 0` 时左侧为 0，因此不会因该不等式失败。

### 晋级、回退与冻结

- 闸门通过：`ps += 1`，`fs` 不变；`ps == K` 时，最后一批进入完成，否则进入下一批并把本批计数、`ps`、`fs` 清零，新的 `st = now`。
- 闸门失败：`fs += 1` 且 `ps = 0`；`fs == R` 时回退。
- 第 0 批触发回退：回退次数加一并中止，实例数为 0。
- 其他批次触发回退：回退次数加一，回到上一批，实例数为上一批的 `cum`，新的 `st = now + H`，本批计数、`ps`、`fs` 清零。
- 若回退次数达到 `M` 且未中止，则进入冻结，冻结后不再接受 `Observe`；中止优先于冻结。

拒绝原因按以下顺序只返回第一个：

1. 参数非法（构造参数、`now`、单次增量、累计上限、错误数大于请求数）。
2. 状态不符（重复 `Start`，或在非发布中调用 `Observe`）。
3. 时钟回退（`now` 小于已接受操作的最大时间）。

被拒绝的操作不会改变任何计数、状态、批号或最大已接受时间。

### 本地验证

如果系统 PATH 没有 `go`，可直接使用 `/usr/local/go/bin/go`；若默认 Go 缓存位于只读目录，可把缓存指到 `/tmp`：

```bash
# 全量测试
GOCACHE=/tmp/go-cache GOPATH=/tmp/go /usr/local/go/bin/go test ./...

# 竞态检测
GOCACHE=/tmp/go-cache GOPATH=/tmp/go /usr/local/go/bin/go test -race ./...

# 随机对照测试的输入、输出、状态与判定依据
GOCACHE=/tmp/go-cache GOPATH=/tmp/go /usr/local/go/bin/go test ./ontology -run TestRandomOperationsAgainstNaive -v

# 格式化与静态检查
/usr/local/go/bin/gofmt -w ontology/rollout.go ontology/rollout_test.go
GOCACHE=/tmp/go-cache GOPATH=/tmp/go /usr/local/go/bin/go vet ./...
```

随机测试固定种子重放 2000 组操作序列，并逐操作对照一个按规则直接编写的朴素状态机；边界测试覆盖去重、浸泡边界、样本边界、`Ef`、严格不等式、零基线流量、连击清零、晋级、完成、回退、中止、冻结、拒绝优先级及累计上限。
