# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 分布式逻辑时钟组件（`clock` 包）

`clock` 包为多个节点上的**本地（local）、发送（send）、接收（receive）**
三类事件打 Lamport 逻辑时间戳，并维护全部事件的**全序**与**因果先后**关系。

### 时钟推进规则

每个节点各自维护一个从 0 开始、单调递增的逻辑时钟：

| 操作 | 规则 |
| --- | --- |
| `Local(node)` | `clock = clock + 1` |
| `Send(node)` | `clock = clock + 1`，同时登记一条携带该时间戳的消息 |
| `Receive(node, msg)` | `clock = max(clock, 消息携带时间戳) + 1` |

- 每个节点的事件序号（`Seq`）从 0 起连续递增；全局事件 ID 从 0 起按
  操作被接受的顺序连续分配。
- 每条发送消息**只能被接收一次**（任一节点）；重复接收一律拒绝。

### 全序规则（与执行先后无关）

`TotalOrder()` 按以下键排序：

1. 逻辑时钟 `Clock` 升序；
2. 时钟并列时按节点编号 `Node` 升序（节点编号小的排前）；
3. 再以全局 ID 兜底（同一节点时钟严格递增，正常不会用到）。

因此全序只取决于事件自身的 `(Clock, Node)`，与操作实际执行/到达的
先后顺序无关；同一输入序列反复计算得到完全相同的输出。该全序与
happened-before 因果关系一致：若 a 因果先于 b，则 a 一定排在 b 前面。

### 因果判定规则

`Compare(a, b)` 返回 `before` / `after` / `concurrent`，依据三类边的
**传递闭包**（内部 BFS，日志中打印完整路径）：

1. **同一节点先后**：同节点上 Seq 小的事件先于 Seq 大的；
2. **发送 → 接收**：发送事件先于其消息对应的接收事件；
3. 以上关系的**传递闭包**。

两个事件互不可达时判为**并发（concurrent）**。

### 非法输入与拒绝原因

以下操作被拒绝时返回 `*clock.Error`，可用 `errors.Is` 按种类区分，
且**不改变任何节点时钟、事件编号、全序或消息登记**：

| 哨兵错误 | `Kind` | 触发条件 |
| --- | --- | --- |
| `ErrNodeOutOfRange` | `node_out_of_range` | 节点编号为负或 ≥ 节点数 |
| `ErrMessageNotFound` | `message_not_found` | 接收了从未登记的消息 |
| `ErrDuplicateReceive` | `duplicate_receive` | 同一条消息被再次接收 |
| `ErrEventLimit` | `event_limit_exceeded` | 已接受事件数达到 `New` 设定的上限 |

校验顺序为：节点编号 → 消息是否存在 → 是否重复接收 → 事件限额；
多个条件同时违反时按此顺序返回第一个错误，且任何拒绝都不改变状态。

### 并发与确定性

- 所有方法均可被多 goroutine 并发调用；校验与状态变更在同一临界区内
  原子完成，故并发下每个节点的事件编号依旧连续，无"半生效"操作。
- 同一批输入无论如何交错、重放多少次，事件时间戳、全序、两两因果
  判定结果完全一致。
- 每次接受/拒绝都会通过可替换的 `Logger` 打印**输入、时间戳与判定
  依据**（如 `max(local=1, message=2)=2 (message) + 1 = 3`、
  传递路径 `event0 -> event1 -> event3`、`concurrent` 等）。
  `SetLogger(nil)` 可关闭日志。

### 最小用法

```go
sys := clock.New(3, 1000) // 3 个节点，最多 1000 个事件
e, err := sys.Local(0)
send, err := sys.Send(0)
recv, err := sys.Receive(1, send.MessageID)

for _, ev := range sys.TotalOrder() { ... }
order, err := sys.Compare(send.ID, recv.ID) // clock.OrderBefore
```

### 本地验证

```bash
# 全量测试（含示例，示例会逐行校验日志中的输入/时间戳/判定依据）
go test -v ./clock/

# 竞态检测 + 重复压测并发用例
go test -race -count=20 ./clock/

# 覆盖率
go test -coverprofile=coverage.out ./clock/
go tool cover -html=coverage.out
```

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
