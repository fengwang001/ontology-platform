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

## 双输入检查点屏障对齐（`barrieralign`）

包 `barrieralign/` 实现双输入算子（通道 0 / 1）的检查点屏障对齐，语义与 Flink
的 aligned checkpoint 一致，保证每张快照恰好包含两侧同号屏障之前的全部记录。

### 事件模型

- `Event{Channel, Key, Value, Barrier}`：`Barrier == 0` 表示记录（`Key` 必须非空），
  非 0 表示该通道的一道屏障。
- 每个通道的屏障编号从 1 开始严格递增。

### 阻塞、对齐、快照与重放

- **阻塞**：通道收到自己当前编号的屏障后立即进入阻塞；屏障之后到达的记录（以及后续
  屏障）进入该通道缓冲，不再累加、不再下发。另一通道若尚未阻塞，其记录仍然到达即
  累加并下发。
- **对齐**：两个通道都阻塞在同一编号屏障时完成对齐：先对当前逐键累加计数拍一张
  不可变快照（因此缓冲中的记录一定不在快照内），向下游转发一道
  `Kind="barrier", Aligned=true` 事件，然后两通道解除阻塞。
- **重放**：缓冲按**跨通道的全局到达顺序**（单条 FIFO 队列）重放，记录被累加并下发；
  重放中遇到任一侧的下一号屏障时立即消费该屏障、该通道重新阻塞并停止重放，等待下一轮
  对齐；若没遇到下一号屏障则缓冲整体排空。
- **并发与确定性**：`Aligner` 内部以 `sync.RWMutex` 保护；`Outputs`、`Snapshots`、
  `Pending` 返回深拷贝的只读视图，写入进行中也可安全并发读取，每张快照逐键自洽。
  处理逻辑不依赖 goroutine 调度或 map 迭代顺序，同一输入序列反复计算得到完全相同的
  输出流与快照。

### 拒绝规则（原因可区分，错误为 `*RejectError`）

| `RejectKind` | 触发条件 |
| --- | --- |
| `invalid_channel` | 通道号不是 0 或 1 |
| `empty_key` | 记录事件的 `Key` 为空 |
| `unexpected_barrier` | 屏障编号不等于该通道下一个期望编号（首号为 1，必须连续递增） |
| `buffer_overflow` | 阻塞通道再缓冲一条记录会超过 `New(bufferLimit)` 的上限 |

`ProcessBatch` 在整份状态的深拷贝上推演批次，任一条事件被拒绝都会丢弃副本整体回滚：
累加计数、缓冲、快照与输出流均保持批前不变（单条 `Process` 同理）。

### 本地验证

```bash
# 详细日志：打印每个用例的输入、输出尾段、缓冲计数、快照与判定依据
go test -race -v ./barrieralign

# 覆盖率
go test -coverprofile=coverage.out ./barrieralign
go tool cover -func=coverage.out
```

用例覆盖：一侧先到屏障后持续缓冲、未阻塞侧即到即处理、对齐快照内容、跨多轮对齐与
重放遇下一号屏障重新阻塞、四类非法输入、被拒批次的原子回滚、重复计算确定性，以及
`-race` 下的并发读取。
