# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 双时钟水位维护器（`watermark` 包）

`watermark.Maintainer` 用两套相互独立的水位分别跟踪事件时间与处理时间，
保证迟到判定只依赖事件时间，处理时间永不干扰、结果可按序重放复现。

### 两套水位

- **事件时间水位（event watermark）**：`已见最大事件时间 - 允许迟到`。
  - 未见过任何事件时为**负无穷**（`View.EventWatermarkSet == false`）。
  - 仅在**按时接受**事件后推进；迟到事件不改变它，也不改变任何其他状态。
  - 单调不减（事件时间回退/重复只会被判迟到，不会拉低水位）。
- **处理时间水位（processing watermark）**：初始为负无穷，**仅由 `Heartbeat` 推进**。
  - 只进不退：心跳时间早于当前水位会被整体拒绝；相等允许。
  - 绝不参与迟到判定，也不改变已接受事件、迟到计数等事件时间状态。

### 迟到判定规则

摄入事件 `(id, eventTime)` 时只读取事件时间水位：

1. `id` 为空 → 整体拒绝（`ReasonEmptyID`），两个水位与计数均不变。
2. `eventTime <= 事件时间水位`（含恰好相等）→ **迟到**：丢弃，仅 `LateCount + 1`，
   不进入已接受视图、不推进任何水位。
3. `eventTime > 事件时间水位`（水位为负无穷时恒成立）→ **按时**：加入视图，
   已见最大事件时间取较大者，事件时间水位随之推进。

判定结果 `IngestResult` 同时回传两个水位与 `Basis` 判定依据字符串。

其他整体拒绝（返回 `*RejectError`，可按 `Reason` 区分，失败不改变任何状态）：

- `negative_allowed_lateness`：`New` 时允许迟到为负。
- `empty_event_id`：摄入事件标识为空。
- `heartbeat_regression`：心跳时间早于当前处理时间水位。

### 并发语义

所有方法以同一把互斥锁串行化，`View` 返回深拷贝快照：

- 摄入与心跳可并发；并发摄入互不相同的按时事件后，视图恰好包含全部事件。
- `View` 查询与 `CheckInvariants` 自检可与任何操作并发，始终观察到一致状态。
- 两个水位各自单调不减；自检会重算最大事件时间与水位并核对计数。

### 本地验证：按序重放

迟到判定是事件日志的纯函数，与墙钟、处理时间、线程调度无关。核对方法：

1. 记录事件摄入日志（`id, eventTime`）与心跳日志（`now`），分两个流保存。
2. 只取事件流，按原顺序在全新维护器上重放：忽略全部心跳。
3. 再带上任意穿插/超前/缺失的心跳重放；两次的已接受事件、迟到计数、
   事件时间水位必须逐项一致（`TestDeterministicOrderedReplay` 固化该性质）。

```bash
# 功能 + 竞态检测（测试日志打印事件、两个水位、判定与判定依据）
GOCACHE=/tmp/gocache go test -race -v ./watermark

# 覆盖率
GOCACHE=/tmp/gocache go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
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
