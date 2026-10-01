# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 主动健康检查状态机

实现在 `healthcheck.go`（类型 `HealthChecker` / `TargetState`），并发安全（互斥锁，
结果等价于某个串行顺序）。构造：

```go
hc, err := ontology.NewHealthChecker(N, R, F, I, FI, DI, initialHealthy, Wf, Q)
```

- `N`：目标数，编号 `0..N-1`；`R`：上升所需连续成功数（1–1000）；`F`：下降所需连续失败数（≥1）。
- `I/FI/DI`：正常 / 快速 / 故障间隔，均在 1–10^9；`Wf`：翻转窗口，1–10^9；`Q`：全局快速名额，0–N。
- 时刻 `now` 与所有间隔为 int64；合法 `now` 范围 `[0, 10^15]`。
- 上述配置任一越界，构造整体返回 `ErrInvalidConfig`。

每个目标维护：状态、连续成功数 `a`、连续失败数 `b`（初值 0）、下次探测最早时刻 `nd`
（初值 0）、转移时刻列表 `tr`（初值空）、加速截止时刻 `fu`（初值 0）。

### 升降计数规则

- 健康态：成功清零 `b`；失败 `b++`，当 `b >= F` 时转为不健康，`a,b` 清零并记录转移。
- 不健康态：失败清零 `a`；成功 `a++`，当 `a >= Reff` 时转为健康，`a,b` 清零并记录转移。
- 不变量：健康态 `a=0`、不健康态 `b=0`；`tr` 严格非降；同一目标 `nd` 随被接受探测严格递增。

### 翻转抑制

转移时刻 `t` 在 `now` 有效当且仅当 `t+Wf > now`（`t+Wf == now` 恰好过期）。

```text
g    = 此刻该目标 tr 中有效转移的个数（不含本次探测刚发生的转移）
Reff = R × (1 + min(g, 4))
```

近期翻转越多，恢复所需连续成功数越高；`g` 超过 4 按 4 封顶。转移随窗口过期后
`Reff` 自动回落。

### 间隔选择与全局快速名额

处理（含转移）完成后，按处理后的状态与计数选候选间隔：

| 状态 | 计数 | 候选间隔 |
| --- | --- | --- |
| 健康 | `b > 0` | `FI`（名额不足回退 `I`） |
| 健康 | `b = 0` | `I` |
| 不健康 | `a > 0` | `FI`（名额不足回退 `DI`） |
| 不健康 | `a = 0` | `DI` |

候选为 `FI` 时，统计其他目标中 `fu > now` 的个数 `x`（自己的旧 `fu` 此时必不大于
`now`，天然不计）：`x >= Q` 时不占名额、改取当前状态常规间隔并置 `fu=0`；否则取
`FI` 并置 `fu = now+FI`。非 `FI` 候选一律置 `fu=0`。最终 `nd = now + 间隔`。
因此每次被接受探测之后，在该 `now` 处 `fu > now` 的目标数不超过 `Q`；`Q=0` 时
永不取 `FI`。

### Probe 拒绝顺序（只报第一个，拒绝不改变任何状态）

1. 目标编号越界：`ErrTargetOutOfRange`
2. `now < 0` 或 `now > 10^15`：`ErrInvalidTime`
3. `now` 小于已接受探测见过的最大 `now`（全局时钟回退，初值 0）：`ErrClockWentBackwards`
4. `now < 该目标 nd`（探测过早；`now == nd` 允许）：`ErrProbeTooEarly`

查询：`State(target)` 仅检查编号越界，返回状态与 `a/b/nd/fu` 及 `tr` 的拷贝；
`Healthy()` 返回健康目标编号升序列表。相同探测序列重放得到完全相同的状态、计数、
`tr` 与 `nd` 序列。

### 本地验证

若 `go` 不在 PATH，先 `export PATH=$PATH:/usr/local/go/bin`；若构建缓存目录只读，
设置 `export GOCACHE=/tmp/gocache`。

```bash
# 全部测试（含 -race）
go test -race -count=1 ./...

# 查看随机对照测试前 3 组的输入/输出/判定依据日志
go test -run TestDifferentialRandom -v .

# 格式化与静态检查
gofmt -l .
go vet ./...
```

测试覆盖：`R=1/F=1` 一次转移、成功清 `b`、失败清 `a`、转移当次按转移后状态选间隔、
翻转窗口边界（相等失效 / 差 1 有效）、`g>4` 封顶、翻转抑制下 `a==R` 不恢复、
`Reff` 随历史过期下降、`Q=0`、名额边界 `x=Q-1/Q`、不健康态名额不足回退 `DI`、
自己的旧 `fu` 不占名额、`now==nd`/`nd-1`、全局时钟回退、初始不健康、四种间隔、
拒绝不改状态、配置非法、并发；并用 `TestDifferentialRandom` 将 2000 组随机探测序列
（含各类拒绝操作）与按规则直写的朴素模型逐步对照，同时校验名额不变量。

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
