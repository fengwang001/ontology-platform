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

## 带优先级的抢占式变更事件处理器

实现在 `ontology/processor.go`，每个变更事件携带全局唯一标识与非负优先级。

### 调度规则

- **优先级降序**：优先级数字越大越先处理；每次 `Advance()` 恰好推进一个事件（一个批次只推进一步）。
- **同优先级先进先出（FIFO）**：同一优先级按 `Enqueue` 的提交顺序处理；抢占只切换批次，不改变同优先级事件的相对顺序。
- **抢占与保存现场**：若下一次推进时存在比当前批次更高优先级的事件，则在最近一个**完整事件边界**打断当前批次，把其“优先级 + 已处理位置”压入保存现场栈，转去处理高优先级批次。
- **恢复顺序（LIFO）**：高优先级批次耗尽后，从栈顶弹出最近被打断的批次，从保存的位置继续；多级抢占时恢复顺序与打断顺序相反。
- **恰好一次**：事件标识全局唯一（已入队、已处理均不可复用），任何事件最多且最终会被处理一次。

### 拒绝原因（整体拒绝、无副作用）

`Enqueue` 为原子批量操作，任何一个事件非法则整批拒绝，队列、当前批次、保存现场、已处理序列均不变；原因可区分：

- `ErrNegativePriority`：存在负优先级（先于重复判定检查）。
- `ErrDuplicateID`：批次内部重复，或与已入队/已处理事件标识重复。
- `ErrNoProcessable`：在没有任何可处理事件时调用 `Advance()`。

### 并发与自检

- `Processed()` / `Snapshot()` / `Check()` 使用读锁，可与写入及彼此并发调用；并发读到的总是某个完整事件边界的前缀（只追加、不改写）。
- `Check()` 校验位置表一致性、保存现场栈单调性、已处理事件唯一性，并重放内部操作日志到新实例逐元素核对。

### 本地验证：排序对照

抢占/恢复的正确性等价于一个更简单的排序规则，测试中的参考模型 `oracle`（见 `ontology/processor_test.go`）每一步都把全部待处理事件按

```
(优先级降序, 到达顺序升序)
```

排序后取第一条。若抢占式处理器的已处理序列与该参考模型**逐元素相同**，即证明“优先级降序 + 同优先级 FIFO + 抢占保存/恢复”全部正确：

```bash
# 竞态检测 + 详细日志（含每次操作的队列、当前批次、保存现场、已处理序列与判定依据）
go test -race -v -run 'TestPreemption|TestResume|TestSamePriority|TestSortOracle' ./ontology

# 600 步确定性操作流与排序参考模型逐步对照，并在结束时核对每个事件恰好一次
go test -v -run TestSortOracleRandomized ./ontology

# 并发读前缀一致性 / 并发自检
go test -race -v -run TestConcurrentReads ./ontology
```

如需手工核对：任意操作序列都可先记录 `Enqueue` 提交顺序，再用上述排序键模拟“每步取最高优先级且同优先级最早到达”的事件，与 `Processed()` 返回的序列逐项比较，结果必须完全一致且可复现。
