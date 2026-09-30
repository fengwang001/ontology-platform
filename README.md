# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 副本弹性伸缩控制器

`autoscaler.Controller` 使用整数毫秒时间和整数指标值，创建时提供：

- `MinReplicas` / `MaxReplicas`：副本数下限与上限，初始副本数为下限。
- `Target`：目标指标值 `T`，必须为正数。
- `WindowMS`：缩容稳定窗口 `W`，单位毫秒，不能为负。
- `CooldownMS`：缩容冷却 `D`，单位毫秒，不能为负。

每次 `Evaluate(now, m)` 先按当前副本数 `c` 计算原始推荐值：

- 当 `|m-T|*10 <= T` 时，指标处于包含边界的 ±10% 容忍区，推荐值为 `c`。
- 否则推荐值为 `ceil(c*m/T)`。
- 推荐值会被夹到 `[MinReplicas, MaxReplicas]`，然后连同 `now` 一起写入历史。

扩容与缩容规则：

- 推荐值大于 `c` 时立即扩容，结果为 `min(recommended, 2c)`，再受上限约束；扩容不受窗口和冷却影响。
- 推荐值不大于 `c` 时，缩容目标为 `min(c, 窗口内最大推荐值)`。
- 窗口包含满足 `now-t <= W` 的历史推荐值，也包含本次推荐值；恰为 `W` 仍保留，`W+1` 移出。
- 目标等于 `c` 时维持；若推荐值低于 `c`，类别为窗口压住。
- 目标低于 `c` 且从未缩容时允许缩容；已经缩容过时，只有 `now-lastScaleDownAt >= D` 才允许再次缩容，恰为 `D` 允许，否则冷却压住。

判定类别：

- `scale_up`：扩容，一次最多翻倍。
- `scale_down`：缩容到窗口内最大推荐值与当前副本数的较小值。
- `maintain`：推荐值不要求降低，副本数不变。
- `window_blocked`：推荐值低于当前副本数，但窗口内旧高值要求保持当前副本数。
- `cooldown_blocked`：窗口目标允许降低，但距离上次实际缩容不足 `D`。

非法创建参数按下限、上限、目标值、窗口、冷却的顺序只返回第一个错误。评估时若时间早于上一次评估，或指标为负，也按该顺序只返回第一个错误，且不改变历史、副本数或上次缩容时间；相同时间戳允许重复评估。所有状态访问由互斥保护，并发调用等价于某个串行交错顺序。成功评估和被拒绝评估都会通过 `slog` 打印输入、输出、判定类别和依据。

示例：

```go
controller, err := autoscaler.New(autoscaler.Config{
    MinReplicas: 1,
    MaxReplicas: 20,
    Target:      100,
    WindowMS:    60_000,
    CooldownMS:  300_000,
})
if err != nil {
    return err
}

result, err := controller.Evaluate(nowMS, metric)
fmt.Println(result.Replicas, result.Decision)
```

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

# 只验证弹性伸缩控制器
go test -race -v ./autoscaler

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
