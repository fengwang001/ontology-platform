# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## notify：通知重试调度器

`notify` 包实现带渠道降级链、Retry-After 与渠道熔断的确定性重试调度器。
所有操作可并发调用（内部串行化），相同操作序列（含同一 jitter 函数）
重放得到完全相同的回报与死信。

### 构造参数

`NewScheduler(Base, Cap, M, A, RAcap, K, Cool, jitter)`：

- `Base`/`Cap`：退避基数（1..1e6）与上限（Base..1e9）。
- `M`：单渠道连续失败上限（1..16）；`A`：任务总失败预算（1..100）。
- `RAcap`：可接受的 Retry-After 上限（0..1e9）。
- `K`/`Cool`：渠道熔断阈值（1..1000）与熔断时长（1..1e9）。
- `jitter(id, n, d)`：注入的确定性抖动函数，返回 int64。

### 退避公式与抖动夹取

第 n 次本渠道连续失败（n < M）时：

```
d  = min(Cap, Base * 2^(n-1))
d' = d - clamp(jitter(id, n, d), 0, floor(d/4))
w  = max(d', ra)        // transient 的 ra 恒为 0
nextAt = now + w        // nextAt > deadline 时死信 expired（恰等于允许）
```

### Retry-After 与切换判定

- `permanent`，或 `throttled` 且 `ra > RAcap`：直接进入切换分支
  （死信原因分别记 `permanent` / `throttled`）。
- 否则 `n++`；`n >= M` 进入切换分支（原因 `exhausted`），否则按上式等待。
- 切换分支：取下标最小且大于 cur、且在 now 未熔断（`openUntil <= now`）
  的渠道；找到则 `cur=j, n=0, w=0`（att 保留），找不到则以记下的原因死信。

### 熔断与死信原因优先级

- 非 permanent 失败使该渠道全局连续失败数 `gfail[ch]++`，达到 K 则
  `openUntil[ch] = now + Cool`；`openUntil > now` 才算熔断中（恰等于不算）。
- `Success` 把当前渠道 `gfail` 清零，但不解除已有熔断。
- 单次 `Fail` 内的判定顺序：切换分支（原因 permanent/throttled/exhausted）
  → 总预算 `att >= A`（原因 `budget`）→ 截止检查（原因 `expired`）。
- 拒绝原因按序只报第一个：参数非法 → 任务不存在 → 任务已结束 →
  时钟回退 → 过早；被拒操作不改变任何状态。

### 本地验证

```bash
go test ./notify            # 规则单测 + 2000 组随机序列对拍朴素模拟
go test -race -v ./notify   # 竞态检测；对拍日志打印输入/输出/判定依据
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
