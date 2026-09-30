# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 磁盘请求调度器（`scheduler` 包）

`scheduler` 包实现了一个带到期保障、可精确复现的磁盘 I/O 调度器。读、写
请求分队列存放，派发时先选方向，再在该方向内按"到期 → 扫描 → 回绕"挑选。
所有方法（`Submit` / `Cancel` / `Dispatch` / `Snapshot`）由同一把互斥锁
保护，可被并发调用。

### 请求与配置

- 请求：`Request{ID, Sector, Op, SubmitAt}`，`Op` 为 `Read` 或 `Write`。
- 磁头初始位置为 0；时间由调用方以单调时钟读数给出（`SubmitAt`、`now`）。
- 配置：`Config{ReadDeadline: Er, WriteDeadline: Ew, WriteStarveX: X}`。
  - `Er`/`Ew`：读/写到期时长，必须为正。
  - `X`：写队列非空期间连续被优先的读派发次数上限，必须 ≥ 1。

### 方向选择（先做）

每次 `Dispatch(now)` 按以下顺序决定方向：

1. 读队列非空 **且**（写队列为空 **或** 饿计数 `< X`）→ 选读；
2. 否则 → 选写。当饿计数已达 `X` 且写队列非空而改选写时，
   `DispatchResult.WriteForced == true`。

### 队内选择（定方向后）

在所选方向的队列内，依次判定：

1. **到期**：该方向按提交先后的队首满足 `now - SubmitAt >= 到期时长`
   （恰等也算到期）→ 直接派发队首，不论其扇区位于何处（可越过磁头位置）。
2. **扫描（SCAN）**：队首未到期时，取扇区不小于磁头位置的最小扇区者；
   同扇区取提交在先者，依据记为 `ReasonScan`。
3. **回绕（wrap）**：不存在不小于磁头位置的请求时，回绕取该方向扇区最小者；
   同扇区取提交在先者，依据记为 `ReasonWrap`。

派发后磁头移动到该请求扇区，请求从全部队列移除；终态（已派发/已取消）被
永久记录，保证每个请求恰好被派发一次或取消一次。

### 饿计数规则

- 派发一个读请求且**此刻写队列非空**：饿计数加一；
- 派发读请求但写队列为空：饿计数**不变**；
- 派发写请求（含被写饿强制派发）：饿计数清零。

因此写队列持续非空时，相邻两次写派发之间的读派发不超过 `X` 次。

### 取消

`Cancel(id, now)` 只能取消尚未派发的在队请求。取消原因与请求终态可区分：

- `ErrNotFound`：标识从未提交，或已被取消；
- `ErrAlreadyDispatched`：标识已派发。

### 拒绝原因与多因顺序

下列情况整体拒绝（返回哨兵错误，且不改变队列、磁头、饿计数）：

- `ErrClockRewind`：时刻早于此前见过的任一时刻（相等不算回拨）；
- `ErrNegativeSector`：扇区为负；
- `ErrDuplicateID`：标识重复（在队、已派发、已取消均算）；
- `ErrInvalidReadDeadline` / `ErrInvalidWriteDeadline` / `ErrInvalidStarveX`：
  配置非法（`New` 时返回）；
- `ErrEmptyQueue`：两队列全空时派发。

多因同时成立时只报第一个：

- 提交：时钟回拨 → 扇区为负 → 标识重复；
- 取消与派发：时钟回拨 → 其余原因。

### 可复现性

选择规则只依赖队列内容、提交次序（同提交时刻由内部单调序号决定先后）、
磁头位置与饿计数，不依赖 wall-clock 或 goroutine 调度；相同的提交、取消与
派发序列（含相同时刻）重放，得到完全相同的派发顺序、依据与强制写标记。

### 日志

`New(cfg, logger)` 接受可选的 `Logger`（实现 `Logf(format, args...)`）。
每次提交、取消、派发都会记录输入、输出与判定依据（`deadline`/`scan`/
`wrap`、`writeForced`、磁头位置、饿计数、队列长度），拒绝时记录拒绝原因。
传入 `nil` 即关闭日志。

### 本地验证

```bash
# 全部测试（带竞态检测）
go test -race -v ./scheduler/

# 全仓库测试与覆盖率
go test -race ./...
go test -cover ./scheduler/

# 格式化与静态检查
gofmt -l scheduler/
go vet ./...
```

如默认构建缓存目录不可写，可指定缓存目录，例如
`GOCACHE=/tmp/gocache go test -race ./...`。

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
