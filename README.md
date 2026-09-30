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

## 磁盘请求调度器（`scheduler` 包）

带到期保障的磁盘请求调度器：读、写各维护一条按提交先后排列的队列，磁头位置初始为
0。配置项 `Config{ReadExpire: Er, WriteExpire: Ew, WriteStarve: X}` 分别为读到期时长、
写到期时长与写饿上限（连续被优先的读次数上限）。

### 方向选择

每次 `Dispatch(now)` 先在两条队列间选方向：

- 读队列非空且（写队列为空 或 饿计数 `< X`）时选读；
- 否则选写；当读队列非空却因饿计数已达到 `X` 而改选写时，结果标记
  `ForcedWrite = true`。

饿计数更新规则：

- 派发读且此刻写队列非空：饿计数加一；
- 派发读而写队列为空：饿计数不变；
- 派发写：饿计数清零。

因此写队列持续非空时，相邻两次写派发之间的读派发不超过 `X` 次。

### 队内选择

在选中的方向内按以下顺序选取（读用 `Er`、写用 `Ew`）：

1. **到期**：该方向按提交先后的队首满足 `now - 提交时刻 >= 到期时长`（恰等也算到期），
   直接派发队首，可越过更靠近磁头的其他请求；
2. **扫描**：否则选取扇区不小于磁头位置的最小扇区者；
3. **回绕**：不存在这样的请求时，回绕选取该方向扇区最小者。

扫描与回绕中扇区相同取提交在先者（提交时刻相同再按内部提交序号），保证结果唯一且可复现。
派发后磁头移到该请求扇区，请求从队列移除，依据在 `DispatchResult.Reason` 中返回
（`expired` / `scan` / `wrap_around`）。

### 拒绝原因与不变量

`Submit` / `Cancel` / `Dispatch` 在同一把互斥锁下执行，可并发调用；每个请求恰好被派发
一次或被取消一次。可区分的错误见 `scheduler.go` 中的哨兵错误：

- 提交：`ErrClockRewind`（时刻早于此前见过的任一时刻）、`ErrNegativeSector`、
  `ErrDuplicateID`，多因同时成立时只按「时钟回拨、扇区为负、标识重复」报第一个；
  另有 `ErrInvalidConfig`（`Er`/`Ew` 不为正或 `X < 1`）；
- 取消：先报 `ErrClockRewind`，从未提交报 `ErrCancelNotFound`，已派发或已取消报
  `ErrAlreadyDispatched`；
- 派发：先报 `ErrClockRewind`，两队列全空报 `ErrNoPendingRequests`。

被拒绝的操作不改变队列、磁头、饿计数与已见最大时刻。相同的提交、取消与派发序列重放
得到完全相同的派发顺序（见 `TestDeterministicReplay`）。

### 本地验证

```bash
# 全部测试
go test ./scheduler

# 竞态检测 + 详细日志（打印每次输入、输出与判定依据）
go test -race -v ./scheduler

# 反复运行并发用例
go test -race -count=30 -run TestConcurrentExactlyOnce ./scheduler

# 格式与静态检查
gofmt -l .
go vet ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
