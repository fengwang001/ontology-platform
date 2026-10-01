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

## 粘性分区器（`sticky` 包）

`sticky/partitioner.go` 实现了无键消息粘性分区器：带键消息按哈希固定分区，
无键消息粘在当前分区直到累计字节达标再切换。所有发送、设置可用性与查询方法
均由互斥锁保护，可并发调用，结果等价于某个串行执行顺序；相同调用序列重放
得到完全相同的分区序列。

### 构造与参数

- `sticky.New(N, B)`：分区数 `N >= 1`、批阈值 `B >= 1`（字节）。
  初始时全部分区可用，粘性分区为 `0`，累计字节为 `0`。
- 参数非法时分别返回 `ErrInvalidPartitionCount`、`ErrInvalidBatchThreshold`。

### 哈希与取余规则（带键消息）

- 对键字节串计算 32 位 FNV-1a：偏移基础值 `2166136261`，
  每个字节先异或再乘以素数 `16777619`，全程 `uint32` 无符号回绕。
- 分区 = `hash mod N`。
- 结果只取决于键与 `N`，与分区是否可用无关，也不计入粘性累计、
  不改变粘性分区与累计字节。空键（含 `nil`）使用 FNV 基础值。

### 粘性切换条件（无键消息）

- 消息大小 `s >= 1`；消息发往当前粘性分区，并把 `s` 累加进累计字节。
- 累加后累计字节 `>= B` 时，本条仍在当前分区发送，发送之后立即把粘性分区
  切换为当前分区之后（环形，不含自身）第一个可用分区，并清零累计。
- 若环形扫描后只有当前分区可用（`N=1` 或其他分区均不可用），则留在当前
  分区，同样清零累计。
- 因此“累计恰等于 B”在本条之后切换；单条 `s > B` 也是本条落当前分区、
  之后立即切换。

### 可用性处理

- `SetAvailable(partition, available)` 可随时调用，本身不触发任何切换，
  也不清零累计。
- 是否切换只在无键消息发送时刻判定：若此刻粘性分区不可用，则发送前先切换
  到环形方向第一个可用分区并清零累计，再执行正常发送与达标判定。
- 标记不可用后又在下一条无键消息发送前恢复可用：不切换、累计保留。
- 分区恢复可用不触发回切。
- 发送无键消息时若没有任何可用分区，返回 `ErrNoAvailablePartition`。

### 拒绝原因（可区分的哨兵错误）

| 情形 | 错误 |
| --- | --- |
| `N < 1` | `ErrInvalidPartitionCount` |
| `B < 1` | `ErrInvalidBatchThreshold` |
| `s < 1` | `ErrInvalidMessageSize` |
| 分区编号越界 | `ErrPartitionOutOfRange` |
| 无键消息发送时无可用分区 | `ErrNoAvailablePartition` |

被拒绝的操作不改变粘性分区、累计字节与可用性。

### 日志

`Partitioner.SetLogger(io.Writer)` 可挂载日志输出；日志逐条打印调用输入、
所选分区与判定依据（累计值、与 `B` 的比较、发送时可用性切换、环形跳过、
拒绝原因及“状态不变”说明）。默认丢弃日志。

### 本地验证

```bash
# 全部测试（含与逐条朴素模拟的随机差分对照）
go test -v ./sticky

# 竞态检测
go test -race ./...

# 覆盖率
go test -coverprofile=coverage.out ./sticky
go tool cover -html=coverage.out
```

测试覆盖：累计恰等于 `B` 时本条之后切换、单条大于 `B`、切换时环形跳过
不可用分区、只有当前分区可用时留在原地、粘性分区被标不可用后的切换时机、
恢复可用不回切、带键消息不计累计且不受可用性影响、各类拒绝保持状态不变、
`N=1` 边界，以及 200 组随机调用序列与独立朴素模拟（含独立 FNV-1a 实现）
逐条对照和重放一致性验证；`go test -v` 日志中可查看每条输入、输出与
判定依据。
