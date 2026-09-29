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

## 带优先级的抢占式变更事件处理器（`eventproc`）

实现在 `eventproc/` 包，用于变更事件的确定性调度。

### 调度规则

- **优先级降序**：事件优先级为非负整数，数值越大越先处理；处理器永远先推进当前可见的最高优先级事件。
- **同优先级 FIFO**：同一优先级严格按到达顺序处理（跨批次同样生效）；一个提交批次中只保留同优先级的连续事件为一个批次，组内先后不变。
- **一次一步**：`Advance()` 每次只推进一个事件（一个批次的一步），处理序列只由提交序列决定，因此始终可复现。
- **抢占与保存现场**：当更高优先级事件到达时，当前批次在**当前事件边界**被打断，其优先级与“已处理位置（cursor）”压入保存现场栈，立即改处理高优先级批次。被打断批次尚未处理的事件不会重复处理。
- **LIFO 恢复**：高优先级批次耗尽后，从栈顶恢复最近被打断的批次，从保存的 cursor 继续；嵌套抢占时栈从底到顶优先级严格递增，逐层恢复。批次恰好在边界耗尽时被抢占不产生空栈帧。
- **恰好一次**：每个事件有全局唯一标识；已处理、等待、当前、栈中的 ID 全部参与去重，每个事件恰好处理一次。

### 拒绝原因（可区分，整体原子）

入队与空转推进失败时返回哨兵错误，可用 `errors.Is` 区分；失败前完成全部校验，**队列、当前批次、保存现场栈、已处理序列均不改变**：

- `eventproc.ErrNegativePriority`：存在负优先级事件。
- `eventproc.ErrDuplicateID`：批次内重复，或与已存在/已处理的 ID 重复。
- `eventproc.ErrEmptyBatch`：提交了零个事件。
- `eventproc.ErrNoProcessable`：`Advance()` 时没有任何可处理事件。

### 并发与自检

- `Processed()` 与 `Snapshot()` 为读锁保护的并发安全查询，返回深拷贝；任意时刻读到的都是某个**完整事件边界**上的前缀，多个读者读到的序列逐元素相同。
- `SelfCheck()` 只读校验内部不变量：cursor 合法、栈优先级严格递增、当前批次严格高于一切待办、已处理 ID 无重复、唯一性集合与实际占用一一对应。
- `Snapshot().String()` 直接打印 `queued / current(cursor) / stack / processed`，单测日志中同时记录操作与判定依据。

### 本地验证方法：排序对照

除结构自检外，`eventproc/processor_test.go` 中的 `TestSortingOracleCrossCheck` 内置了一个**独立于实现**的参考模型：不模拟栈，而是在每次推进前把所有可见批次的队头收集为候选，按

```
优先级降序 → 到达序号升序（同优先级 FIFO）→ 当前/恢复批次优先
```

排序取第一名，逐步与真实处理器的输出对照。本地执行：

```bash
# 全量测试（竞态检测）
go test -race -v ./eventproc/

# 只看排序对照与抢占场景日志
go test -race -v -run 'TestSortingOracleCrossCheck|TestPreempt|TestNested' ./eventproc/

# 静态检查
go vet ./...
```

对照一致即说明“优先级降序 + 同优先级 FIFO + 抢占恢复”的实现与独立排序模型等价。
