# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 无键消息粘性分区器（`partitioner` 包）

`partitioner` 提供并发安全的粘性分区器：带键消息按哈希固定分区，无键消息粘在当前分区直到累计字节达标再切换，每条消息的分区选择与切换时机可精确复现。

### 带键消息：哈希与取余

- 对键字节串计算 **32 位 FNV-1a** 哈希（无符号）：偏移基础值 `2166136261`，素数 `16777619`，逐字节 `h = (h ^ b) * 16777619`（uint32 溢出回绕）。
- 分区 = `hash % N`，只取决于键与 N，**与分区可用性无关**，且**不计入**粘性累计。

### 无键消息：粘性切换条件

- 无键消息（大小 `s >= 1`）发往当前粘性分区，并把 `s` 累加进该分区的累计字节。
- 累加后累计字节 **>= B** 时本条之后立即切换：目标为当前分区**之后（环形）第一个可用分区**；若只有当前分区可用则留在原地。切换后累计清零。
- 初始全部分区可用，粘性分区为 0 号。

### 可用性处理

- 调用方可随时通过 `SetAvailable(partition, available)` 变更可用性。
- 是否切换只在**无键消息发送时刻**判定：若此刻粘性分区不可用，发送前先切换到环形第一个可用分区并清零累计；标记后又在下一条发送前恢复则不切换、累计保留。
- 分区恢复可用**不触发回切**。
- 发送时没有任何可用分区：整体拒绝（`ErrNoAvailablePartition`）。

### 错误

以下情形整体拒绝且**不改变**粘性分区、累计字节与可用性，原因可区分：

| 情形 | 错误 |
| --- | --- |
| N < 1 | `ErrInvalidPartitionCount` |
| B < 1 | `ErrInvalidBatchSize` |
| s < 1 | `ErrInvalidMessageSize` |
| 分区编号越界 | `ErrPartitionOutOfRange` |
| 无键发送时无可用分区 | `ErrNoAvailablePartition` |

### 并发与确定性

发送、设置可用性与查询均可并发调用，结果等价于某个串行顺序；相同调用序列重放得到完全相同的分区序列。

### 本地验证

```bash
go test ./partitioner            # 全部用例（含与朴素模拟的逐条对拍）
go test -race -v ./partitioner   # 竞态检测 + 打印输入/输出/判定依据
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
