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

## BBR 式控制器

根包提供 `New(mss) (*bbr.Controller, error)`，文件为 `bbr.go`。所有确认与查询都由读写锁保护，并发结果等价于某个串行执行顺序；拒绝确认不会改变任何状态或时钟。

### 确认处理次序

`OnAck(now, n, rtt, ds, inflight, appLimited)` 严格按以下顺序执行：

1. 校验参数、时钟回退和样本快照；错误优先级为参数非法、时钟回退、样本非法。参数非法包含累计后 `D+n > 10^12`。
2. 累加 `D += n`；当 `ds >= N` 时进入下一轮，更新 `R++`、`N=D`，并计算 `s=floor((D-ds)*1000/rtt)`。
3. 淘汰满足 `R-r >= 10` 的轮次采样；仅非应用限速，或应用限速但 `s` 不小于当前窗口最大值时入队。
4. 最小时延在为空、`rtt <= minRtt`、或 `now-stamp >= 10000` 时刷新；相等样本会刷新时间戳，恰好在 10000ms 时也判定过期并允许替换为更大 RTT。
5. 仅在新一轮、非应用限速且尚未满管道时检查增长：`maxBw*100 >= fullBw*125` 算增长，相等也算增长；连续三次不增长才置位满管道。Startup 满管道后立即进入 Drain。
6. Drain 在同一次确认中若 `inflight <= BDP`，立即进入 ProbeBW，并设置 `ci=2`、`cs=now`。只有确认开始时已经是 ProbeBW，才会在本次执行周期推进。
7. ProbeRTT 在过期时进入；进入当次若 `inflight <= 4*mss` 立即设置 `pd=now+200`，`now >= pd` 的当次退出。已填满管道时回 ProbeBW（`ci=2`），否则回 Startup。

### ProbeBW 增益表

周期下标使用 `[125, 75, 100, 100, 100, 100, 100, 100]`，每次确认最多推进一格：

- `100`：`elapsed >= minRtt` 即推进。
- `125`：`elapsed >= minRtt` 且 `inflight >= floor(BDP*125/100)` 才推进。
- `75`：`elapsed >= minRtt` 或 `inflight <= BDP` 即推进。

### 输出公式

- `BDP=floor(maxBw*minRtt/1000)`，中间乘积按 128 位处理并向下取整。
- `Pacing=floor(maxBw*pg/100)`：Startup 为 289，Drain 为 35，ProbeBW 为当前周期增益，ProbeRTT 为 100。
- 非 ProbeRTT 的 `Cwnd=max(floor(BDP*cg/100), 4*mss)`：Startup/Drain 为 289，ProbeBW 为 200。
- ProbeRTT 的 `Cwnd=4*mss`；尚未确认时 `Pacing=0`、`Cwnd=10*mss`。

### 本地验证

```bash
# 常规测试
go test ./...

# 竞态检测
go test -race ./...

# 查看随机朴素模型的输入、输出与判定依据
go test -run TestRandomSequencesMatchNaiveModel -v

# 静态检查
go vet ./...
```

测试覆盖 125% 相等边界、应用限速采样、10 轮滤波器边界、最小时延刷新/过期、Startup→Drain→ProbeBW 同次迁移、三种 ProbeBW 增益推进、ProbeRTT 当次定终点与准点退出、拒绝原因优先级、并发查询、`filterOps <= 3*确认次数`，以及 2000 组随机序列与独立朴素模拟器逐项对照。
