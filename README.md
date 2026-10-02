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

## bbr 包：按轮次推进的 BBR 式带宽时延估计与状态机控制器

`bbr`（见 `bbr/bbr.go`）实现了一个确定性的 BBR 式控制器：`New(mss)` 构造（`mss` 取值 1..65535），每个确认事件调用 `OnAck(now, n, rtt, ds, inflight, appLimited)`，查询接口为 `Pacing()`、`Cwnd()`、`State()`、`MaxBw()`、`MinRtt()`、`Round()`。所有方法可并发调用，结果等价于某个串行顺序；相同确认序列重放得到完全相同的状态、速率与窗口。

### OnAck 的处理次序

每次确认按固定七步处理，全部整数运算、除法向下取整：

1. **交付与轮次**：`D += n`；若 `ds >= N` 则 `R += 1`、`N = D`，本次记为 newRound；带宽样本 `s = floor((D-ds)*1000/rtt)`。
2. **带宽滤波器**：先淘汰 `R-r >= 10` 的采样，再当 `appLimited` 为假或 `s` 不小于窗口当前最大值（空窗口按 0）时，把 `s` 记为轮次 `R` 的采样；`maxBw` 取窗口内最大值（空为 0）。
3. **最小时延**：`expired` 当且仅当 `minRtt` 非空且 `now-stamp >= 10000`；若 `minRtt` 为空、或 `rtt <= minRtt`、或 `expired`，则 `minRtt = rtt`、`stamp = now`（相等也刷新；过期时即使 `rtt` 更大也替换）。
4. **满管道检测**：仅当 newRound 且非 appLimited 且未 filled：`maxBw*100 >= fullBw*125`（相等即算增长）则 `fullBw = maxBw`、`fullCnt = 0`，否则 `fullCnt += 1`，到 3 置 filled；随后若状态为 Startup 且已 filled，进入 Drain。
5. **Drain 退出**：若状态为 Drain 且 `inflight <= BDP`（`BDP = floor(maxBw*minRtt/1000)`，乘积用 128 位），进入 ProbeBW 并令 `ci=2`、`cs=now`；第 4、5 步可在同一次确认内连续成立。
6. **ProbeBW 增益循环**：仅当本次确认开始时状态已是 ProbeBW 才执行，`el = now - cs`，每次确认至多推进一格（推进即 `ci=(ci+1)%8`、`cs=now`）。
7. **ProbeRTT**：若 `expired` 且状态不是 ProbeRTT，进入 ProbeRTT 并令 `pd=0`；随后（含刚进入的当次）若 `pd=0` 且 `inflight <= 4*mss` 则 `pd = now+200`；若 `pd != 0` 且 `now >= pd` 则退出：`stamp = now`，filled 为真回 ProbeBW（`ci=2`、`cs=now`）否则回 Startup，`pd=0`。

### 滤波器窗口与过期边界

- 采样记于轮次 `r`，当 `R-r < 10` 时有效，`R-r == 10` 恰好在第 2 步开头被淘汰。
- 滤波器用单调队列实现（队首即窗口最大值），非导出计数器 `filterOps`（入队、出队、淘汰各计 1 次）摊还不超过 `3 x 确认次数`，测试在 1000 与 100000 次确认两组下断言。
- `minRtt` 在 `now-stamp >= 10000` 时过期（恰好 10000 即过期），过期时以更大的 `rtt` 也强制替换；`rtt` 相等也会刷新 `stamp`。

### ProbeBW 增益表与推进条件

增益表为 `[125, 75, 100, 100, 100, 100, 100, 100]`，进入 ProbeBW 时 `ci=2`：

| g   | 推进条件                                                     |
|-----|--------------------------------------------------------------|
| 100 | `el >= minRtt`                                               |
| 125 | `el >= minRtt` 且 `inflight >= floor(BDP*125/100)`           |
| 75  | `el >= minRtt` 或 `inflight <= BDP`                          |

### 速率与窗口公式

- `Pacing() = floor(maxBw*pg/100)`：`pg` 在 Startup 取 289、Drain 取 35、ProbeBW 取当前增益 `g`、ProbeRTT 取 100；尚未接受任何确认时为 0。
- `Cwnd()`：ProbeRTT 为 `4*mss`，其余为 `max(floor(BDP*cg/100), 4*mss)`，`cg` 在 Startup 与 Drain 取 289、ProbeBW 取 200；尚未接受任何确认时为 `10*mss`。

### 错误与拒绝语义

非法输入按 **参数非法（`ErrInvalidParam`）→ 时钟回退（`ErrClockRewind`）→ 样本非法（`ErrInvalidSample`）** 的顺序只报第一个，可用 `errors.Is` 区分；被拒绝的确认不改变 D、轮次、滤波器、时延、状态与时钟中的任何一项。

### 本地验证

```bash
# 专项单测（边界、增益、ProbeRTT、校验顺序等）
go test ./bbr/ -v

# 与朴素模拟对照 2000 组随机确认序列（日志打印输入、输出与判定依据）
go test ./bbr/ -run TestAgainstNaiveSimulation -v

# filterOps 上限断言
go test ./bbr/ -run TestFilterOpsBound -v

# 竞态检测（并发调用等价于某个串行顺序）
go test -race ./bbr/
```
