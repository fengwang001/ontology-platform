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

## 弹性伸缩控制器（`scaler` 包）

`scaler.Scaler` 是一个带稳定窗口与缩容冷却的副本数控制器，所有时间均为整数毫秒。

### 创建与参数校验

```go
s, err := scaler.New(minReplicas, maxReplicas, target, windowMillis, cooldownMillis)
```

- `minReplicas`：副本下限，初始副本数等于下限，必须 `>= 1`。
- `maxReplicas`：副本上限，必须 `>= minReplicas`。
- `target`：目标值 `T`，必须为正。
- `windowMillis`：缩容稳定窗口 `W`，必须非负。
- `cooldownMillis`：缩容冷却 `D`，必须非负。

校验按上述顺序只返回第一个错误（`scaler.ErrMinReplicas` 等）。可用
`scaler.WithLogger(io.Writer)` 重定向评估日志（传 `nil` 关闭）。

### 推荐值计算

每次 `Evaluate(now, m)` 以当前副本数 `c` 计算原始推荐值：

- 若 `|m-T|*10 <= T`（偏差恰为 10% 也算），推荐值 `r = c`；
- 否则 `r = ceil(c*m/T)`；
- 随后把 `r` 夹到 `[minReplicas, maxReplicas]`，夹取后的结果记入历史。

（对整数而言 `|m-T|*10 <= T` 等价于 `|m-T| <= floor(T/10)`，实现采用后者避免乘法溢出。）

### 判定流程与类别

- `scale_up`：夹取后 `r > c`，新副本数为 `min(r, 2c)`，一次扩容最多翻倍；扩容不看窗口与冷却。
- `r <= c` 时先求缩容目标 `goal = min(c, maxWindow)`，其中 `maxWindow` 为历史中满足
  `now-t <= W` 的全部推荐值（含本次）的最大值。恰差 `W` 仍在窗口内，差 `W+1` 移出。
- `hold`：`goal == c` 且 `r == c`（在容忍带内或已等于推荐值）。
- `window_blocked`：`goal == c` 但 `r < c`，即窗口内的高推荐值压住了缩容。
- `cooldown_blocked`：`goal < c` 但距上次实际缩容不足 `D` 毫秒（从未缩容则不受限）；恰差 `D` 允许缩容。
- `scale_down`：`goal < c` 且冷却已过，副本数变为 `goal`，并记录本次缩容时刻。

`Evaluate` 的入参校验：`now` 早于上一次评估时刻返回 `scaler.ErrTimeRegression`
（相等允许），`m < 0` 返回 `scaler.ErrNegativeMetric`，按此顺序只报第一个；
被拒绝的调用不改变历史、副本数与上次缩容时刻。`Replicas()` 与
`LastScaleDownAt()` 为只读查询。所有方法通过读写锁保证并发安全，等价于某个串行顺序。

### 本地验证

```bash
# 全部测试（含 -race 竞态检测与详细日志）
go test -race -v ./scaler/

# 重复运行确认确定性
go test -race -count=20 ./...

# 覆盖率
go test -cover ./scaler/
```

测试覆盖：10% 容忍边界（含 `T` 不能被 10 整除的情形）、翻倍限速、
`W`/`W+1` 窗口边界、`D`/`D-1` 冷却边界、窗口高值压住缩容、上下限夹取、
非法输入不改变状态、相同序列重放确定性、并发评估与查询。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
