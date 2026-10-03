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

## 通知重试调度器（`retry` 包）

带渠道降级链、Retry-After 与渠道熔断的确定性重试调度器。所有判定只依赖构造参数、
注入的 `jitter(id, n, d)` 与操作序列，相同输入重放得到完全相同的回报与死信。

### 退避与抖动

- 第 `n` 次同渠道连续失败的基础退避 `d = min(Cap, Base * 2^(n-1))`，达到 `Cap` 后不再增长。
- 实际退避 `d' = d - clamp(jitter(id, n, d), 0, floor(d/4))`：抖动为负按 0 计，
  超过 `floor(d/4)` 按 `floor(d/4)` 计。
- 等待时长 `w = max(d', ra)`（仅 `throttled` 允许非零 `ra`），下次尝试时刻 `nextAt = now + w`。

### Retry-After 与渠道切换

- `throttled` 且 `ra > RAcap` 直接进入切换分支（死信原因记 `throttled`）；`ra == RAcap` 仍等待。
- `permanent` 直接进入切换分支（原因记 `permanent`）。
- 其余情况 `n` 加一，`n >= M` 进入切换分支（原因记 `exhausted`），切换后立即生效（`w = 0`）。
- 切换选择下标最小且大于 `cur`、在 `now` 未熔断（`openUntil <= now`）的渠道；
  找到则 `cur` 更新、`n` 清零（`att` 保留），找不到则以记下的原因死信。

### 熔断与死信优先级

- 非 `permanent` 失败使该渠道全局计数 `gfail` 加一，`gfail >= K` 时熔断至 `now + Cool`；
  `openUntil == now` 不算熔断。`Success` 把当前渠道 `gfail` 清零，但不解除已有熔断。
- 单次 `Fail` 内的死信优先级：切换原因（`permanent`/`throttled`/`exhausted`）先于
  `budget`（`att >= A`），`budget` 先于 `expired`（`nextAt > deadline`）。
- 拒绝原因按序只报第一个：参数非法 → 任务不存在 → 已结束 → 时钟回退 → 过早；
  被拒绝的操作不改变任何任务、`gfail`、`openUntil` 与最大时钟。
- 所有方法可并发调用（内部互斥），结果等价于某个串行顺序。

### 本地验证

```bash
go test ./retry/            # 单元测试 + 2000 组随机序列与朴素模拟对拍
go test -race ./retry/      # 竞态检测
go test -v -run TestSpecExample ./retry/   # 查看规格示例的逐步日志（输入/输出/判定依据）
```
