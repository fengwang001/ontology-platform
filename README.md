# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 无键消息粘性分区器

`stickypartition` 包提供可并发调用、可确定性重放的消息分区器：

```go
p, err := stickypartition.New(partitionCount, batchThresholdBytes)
partition, err := p.SendWithKey([]byte("order-42"))
partition, err = p.SendKeyless(messageSizeBytes)
err = p.SetAvailable(partitionID, true)
state := p.SnapshotState()
```

初始状态为：分区 `0..N-1` 全部可用，粘性分区为 `0`，累计字节为 `0`。

### 带键消息

带键消息始终按 32 位 FNV-1a 哈希取余：

1. 哈希初始值为 `2166136261`，素数为 `16777619`，运算使用无符号 32 位整数回绕。
2. 对键的每个字节依次执行 `hash ^= byte`、`hash *= 16777619`。
3. 分区编号为 `hash % N`。
4. 即使目标分区当前不可用也返回该编号；带键消息不改变粘性分区和累计字节。

空字节切片仍是带键消息，其哈希为初始值 `2166136261`。

### 无键消息

无键消息使用消息大小 `s`（字节）参与粘性累计：

1. 发送前，如果当前粘性分区不可用，先从当前分区的下一个分区开始环形查找第一个可用分区，切换过去并清零累计。
2. 消息发往当前粘性分区，返回该分区编号，然后把 `s` 加到累计字节。
3. 如果累计字节 `>= B`，本条消息已经发送完成；随后清零累计，并从当前分区的下一个分区开始环形查找第一个可用分区。
4. 累计恰好等于 `B` 也触发切换；单条消息大于 `B` 时仍先发往当前分区，再切换。
5. 如果只有当前分区可用，环形查找会回到当前分区，因此表现为留在原分区并清零累计。
6. 分区恢复可用不会自动回切；只有下一次无键消息需要发送前切换或累计达标切换时才移动粘性分区。

### 可用性与拒绝原因

可用性可通过 `SetAvailable(partition, available)` 随时修改。设置接口只记录状态，不立即触发切换；如果分区被标记不可用后又在下次无键消息前恢复，则不会切换，累计字节保留。

以下操作返回可区分的哨兵错误，并且不会修改粘性分区、累计字节或可用性：

- `ErrInvalidPartitionCount`：`N < 1`
- `ErrInvalidThreshold`：`B < 1`
- `ErrInvalidMessageSize`：无键消息 `s < 1`
- `ErrPartitionOutOfRange`：设置可用性的分区编号不在 `[0, N)`
- `ErrNoAvailablePartition`：无键消息发送时没有任何可用分区

所有发送、可用性设置和查询都由互斥锁串行化。对同一初始状态重放完全相同的调用序列，会得到完全相同的分区序列和状态转移；带键消息的结果只取决于键与 `N`。

### 本地验证

测试包含精确达标、单条超阈值、环形跳过不可用分区、不可用后的延迟切换、恢复不回切、带键消息隔离、拒绝操作原子性、相同脚本确定性重放、逐条朴素模拟对照，并在 `-v` 日志中打印输入、输出、状态和判定依据。

```bash
go test -v ./stickypartition
go test -race ./stickypartition
go test ./...
```

如果当前环境的默认 Go 构建缓存位于只读目录，可指定临时缓存：

```bash
GOCACHE=/tmp/ontology-go-cache go test -race -v ./stickypartition
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
