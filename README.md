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

## 等宽增量直方图（`histogram` 包）

`histogram` 包提供并发安全的等宽分桶增量直方图（`histogram.New(width, upperBound, maxActiveBuckets)`）。

### 桶归属边界规则

- 值域为左闭右开区间 `[0, upperBound)`，按 `width` 等宽切分；`upperBound` 必须是 `width` 的正整数倍，否则 `New` 返回 `ErrInvalidUpperBound`。
- 第 `i` 个常规桶覆盖 `[i*width, (i+1)*width)`；值恰好等于某桶左边界时归入该桶，例如 `width=5` 时 `5` 归桶 `[5,10)` 而非 `[0,5)`。桶下标按整数除法 `value / width` 计算。
- 值达到或超过 `upperBound`（即 `value >= upperBound`）一律进入独立的溢出桶（overflow），下标为 `NumBuckets`，下界 `Lower == upperBound`、`Overflow == true`、`Upper == 0` 表示无上界；无论多大都不新建桶。
- 负值非法：`Add`、`Retract`、`BucketOf` 均返回 `ErrNegativeValue`。

### 计数、撤回与拒绝原因

- `Add(value)` 使归属桶计数加一；`Retract(value)` 减一。计数减到零时该桶立即从直方图中删除，`BucketOf` 返回 `ErrBucketNotFound`，`Buckets()` 快照中也不存在该桶——查询返回“不存在”而不是零。
- 活跃桶（含溢出桶）数量达到 `maxActiveBuckets` 时，向新桶 `Add` 返回 `ErrTooManyBuckets`；向已存在的桶继续加值不受限。桶撤空后槽位释放，可再开新桶。
- 撤回当前不存在（已为空）的桶返回 `ErrBucketNotFound`。
- 所有失败都在任何状态变更前判定并返回，错误均可用 `errors.Is` 区分：`ErrInvalidWidth`、`ErrInvalidUpperBound`、`ErrInvalidMaxBuckets`、`ErrNegativeValue`、`ErrBucketNotFound`、`ErrTooManyBuckets`；一次失败不会改变直方图。
- `sync.RWMutex` 保护内部计数：加/撤互斥，计数与桶查询可并发读；`Buckets()` 返回按下标排序的时点快照副本。

### 本地验证方法

```bash
# 带竞态检测的全量测试（-v 可看到每条输入的桶归属、计数与判定依据日志）
go test -race -v ./histogram
```

核对思路：对每组“加值 + 撤值”操作做正负相抵，再按桶下标分组求和：

- 串行参照：测试在并发加值时用互斥锁维护一个 `map[桶下标]期望值`，并发结束后逐桶与 `BucketOf` 的 `Count` 比对，必须完全一致（见 `TestConcurrentAddDifferentBuckets`）。
- 正负相抵：每个 `Add(v)` 都配一个 `Retract(v)`，全部结束后每个桶的分组计数必须为零，表现为所有桶消失、`Buckets()` 为空（见 `TestConcurrentAddRetractBalance`）。
- 并发一致性：多个 goroutine 同时调用 `Buckets()`，比对快照逐字段（`Index/Lower/Upper/Overflow/Count`）相同（见 `TestConcurrentReadsAreIdentical`）。
- 边界样例：`width=5, upperBound=20` 下检查 `0` 归 `[0,5)`、`5/10/15` 归各自左边界桶、`19` 归 `[15,20)`、`20/21/1000000` 均归溢出桶（见 `TestBucketBoundaries`，测试日志会打印输入值、桶归属、计数与判定依据）。
