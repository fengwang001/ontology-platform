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

## 事件时间重排缓冲（`reorder` 包）

`reorder.Buffer` 是一个有界乱序（bounded out-of-orderness）的事件时间重排组件：
乱序到达的事件按事件时间从**主输出**严格有序交出；太晚到达的事件立即走**旁路输出**单独
交出；每个被接受事件恰好出现一次（既不丢也不重）。全部方法可并发调用。

### 核心概念

- **到达序号（seq）**：每个被接受事件（含旁路事件）在临界区内分配一个从 1 开始的连续
  到达序号，用作同时间事件的稳定排序键与“恰好一次”依据。
- **水位线（watermark）**：`watermark = 迄今见到的最大事件时间 - maxOutOfOrderness`，
  且**单调不减**（不随较小的事件时间回退）。尚未接受任何事件时为 -1。
- **迟到判定**：事件到达时若 `event.Time <= watermark`，判定为迟到，**立即**进入旁路
  输出（按到达顺序记录），不进入缓冲、不参与主输出。
- **缓冲与释放**：非迟到事件进入缓冲；每次接受事件后若水位线推进，则把缓冲中所有
  `event.Time <= watermark` 的到期事件一次性释放，按 **(事件时间, 到达序号)** 稳定排序
  追加到主输出，因此主输出全局严格有序。`maxOutOfOrderness == 0` 时事件在当次即到期释放。
- **排空**：流结束时调用 `Flush()`，把仍滞留在缓冲中的事件按相同排序全部交到主输出
  （不改变水位线）。

### 拒绝原因（均可区分，且不改变任何状态）

被拒绝的操作不会推进水位线、不消耗序号、不改动缓冲或两路输出：

| 错误 sentinel | 原因码 `ReasonOf(err)` | 触发条件 |
| --- | --- | --- |
| `ErrInvalidParam` | `invalid_param` | `capacity < 1`、`maxOutOfOrderness < 0`，或事件时间为负 |
| `ErrEmptyID` | `empty_id` | 事件标识为空串 |
| `ErrDuplicateID` | `duplicate_id` | 标识与此前任一**被接受**事件重复（重复判定优先于迟到判定） |
| `ErrBufferFull` | `buffer_full` | 事件非迟到、需进入缓冲，但按新水位线释放到期事件后仍超过容量 |

### 用法

```go
b, _ := reorder.NewBuffer(1000 /*capacity*/, 5000 /*maxOutOfOrderness*/, slog.Default())
if seq, err := b.Accept(reorder.Event{ID: "evt-1", Time: 12345, Payload: nil}); err != nil {
    switch {
    case errors.Is(err, reorder.ErrBufferFull):
        // 缓冲超限：该事件未被接受，可重试或上报
    case errors.Is(err, reorder.ErrDuplicateID):
        // 重复标识
    }
    return
} else {
    _ = seq // 被接受事件的连续到达序号（迟到事件同样有序号）
}
b.Flush()
main := b.MainOutput() // 按 (time, seq) 严格有序
late := b.SideOutput() // 迟到事件，按到达顺序
```

> 迟到不是错误：`Accept` 对迟到事件返回正常的序号，事件可从 `SideOutput()` 取得。

组件通过传入的 `*slog.Logger` 对每次接受/拒绝/排空打印日志，包含输入事件、判定结果
（`buffered` / `late_side_output` / `rejected`）、判定依据（与水位线的比较或拒绝原因）、
当次释放的 ID 列表及两路输出计数。

### 并发与确定性语义

- 单一互斥保护全部状态，`Accept` / `Flush` / 各读取方法均可并发调用。
- 并发下每个被接受事件在“主输出 + 旁路输出”中恰好出现一次，主输出严格有序。
- 组件不含时间、随机等外部依赖：同一输入序列在独立 `Buffer` 上反复计算，水位线、两路
  输出与待发缓冲完全一致。

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 仅重排组件
go test -race -v ./reorder
go test -run TestConcurrent -race ./reorder

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

`reorder` 包测试覆盖：相同事件时间按到达序号的稳定排序、迟到走旁路、四类非法输入、
拒绝操作前后状态（水位线/序号/缓冲/两路输出）完全不变、乱序下不丢不重、一次性批量释放
的有序性、同一序列反复计算结果一致，以及并发下的恰好一次与严格有序。`-v` 输出中每个
用例都会打印输入、两路输出、待发缓冲/水位线与组件内部日志（含判定依据）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
