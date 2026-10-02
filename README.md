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

## healthcheck 包

`healthcheck` 实现带翻转抑制与全局快速探测名额的主动健康检查状态机（`Checker`，并发安全，所有方法等价于某个串行顺序）。

### 构造参数

`New(Config)` 的任一字段非法即整体拒绝（`ErrInvalidConfig`）：`N>=1`，`R` 为 1..1000，`F>=1`，`I/FI/DI/Wf` 为 1..10^9，`Q` 为 0..N。每个目标维护：状态、连续成功数 `a`、连续失败数 `b`（初值 0）、下次探测最早时刻 `nd`（初值 0）、转移时刻列表 `tr`（初为空）与加速截止时刻 `fu`（初值 0）。

### 升降计数规则

- 健康时：成功清 `b`；失败 `b+1`，`b>=F` 时转为不健康并清零 `a`、`b`。
- 不健康时：失败清 `a`；成功 `a+1`，`a>=Reff` 时转为健康并清零 `a`、`b`。
- 翻转抑制：`Reff = R × (1 + min(g, 4))`，`g` 为当前时刻 `now` 下 `tr` 中仍有效的转移数（`t+Wf > now` 才有效，不含本次）；近期转移越多恢复越难，转移过期后 `Reff` 回落。
- 每次转移（任一方向）把 `now` 追加进 `tr`。

### 间隔选择与快速名额

处理完探测后按新状态选候选间隔：健康且 `b>0` 取 `FI`，健康且 `b=0` 取 `I`，不健康且 `a>0` 取 `FI`，不健康且 `a=0` 取 `DI`。候选为 `FI` 时统计其他目标中 `fu > now` 的个数 `x`：`x >= Q` 时改取该状态常规间隔（健康 `I`/不健康 `DI`）并置 `fu=0`，否则取 `FI` 并置 `fu = now+FI`；取非 `FI` 时 `fu` 置 0。`nd = now + 最终间隔`。

### 拒绝规则

`Probe` 按顺序只报第一个原因，被拒绝的操作不改变任何状态：`ErrTargetOutOfRange`（目标越界）→ `ErrInvalidTime`（`now` 不在 0..10^15）→ `ErrClockRegression`（`now` 小于已接受探测的最大 `now`，初值 0）→ `ErrProbeTooEarly`（`now < nd`）。`State` 只检查目标编号，返回状态拷贝；`Healthy` 返回健康目标编号升序列表。

### 本地验证

```bash
# 规则单测 + 2000 组随机序列对朴素模拟的差分对照（-v 打印输入/输出/判定依据）
go test -v ./healthcheck/

# 并发等价串行顺序（竞态检测）
go test -race ./healthcheck/
```
