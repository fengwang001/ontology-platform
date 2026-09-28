# causalbuf — 基于向量时钟的因果交付缓冲

把乱序甚至重复到达的广播消息按因果顺序交付给上层：满足条件的立即交付，
暂不满足的进入有界缓冲等待前驱，重复消息丢弃并计数。交付既不超前也不遗漏。

## 数据模型

- 系统中有 `peers` 个发送方，编号 `0 .. peers-1`。
- 每条消息 `Message{Sender, Vector, Payload}`：
  - `Sender`：发送方编号；
  - `Vector`：发送时刻的向量时钟，长度等于 `peers`；
  - `Vector[Sender]` 即该发送方的消息序号（从 1 开始递增）。
- 接收端维护本地向量 `vc`：`vc[i]` 表示发送方 `i` **已交付**的最新消息序号，初始全 0。
- 缓冲 `pending`：以 `(Sender, Vector[Sender])` 为键暂存尚不能交付的消息，容量有界。

## 交付条件（deliverable）

对发送方为 `s` 的消息 `m`，**同时**满足以下两条才可交付：

1. **序号恰好接上**：`m.Vector[s] == vc[s] + 1`
   —— 不超前（跳过中间消息）也不落后（旧消息）；
2. **因果前驱均已交付**：对所有 `j != s`，`m.Vector[j] <= vc[j]`
   —— 消息发送时已知的每条其他发送方消息都必须已经交付。

任一不满足则进入缓冲（缓冲满时拒绝，见下）。

## 级联交付

一条消息交付（`vc[s]` 前进）后，缓冲中原先被它阻塞的消息可能变为可交付：

1. 交付触发消息，推进 `vc`；
2. 扫描 `pending`，收集当前全部可交付消息；
3. **按发送方编号最小者依次交付**（同一发送方至多有一条可交付消息，即其下一序号），
   每交付一条就推进 `vc`；
4. 重复 2–3，直到一轮中没有可交付消息为止。

按发送方编号排序是确定性裁决：无论到达时序与并发交织如何，相同的消息集合
总是产生完全相同的交付序列。

## 重复判定（不是错误）

消息身份为 `(sender, seq)`。满足以下任一条件即为重复，**丢弃并令重复计数 +1**，
返回 `OutcomeDuplicate`，且不改变本地向量、缓冲与交付序列：

- `seq <= vc[sender]`：该消息（或其后续）已经交付；
- `(sender, seq)` 已存在于 `pending` 中：重复的在途副本。

即使重复副本的其余向量分量或负载不同，也按身份键判重丢弃。

## 拒绝原因（可区分，拒绝不改变任何状态）

| 错误哨兵 | 触发条件 |
| --- | --- |
| `ErrInvalidArgument` | `New` 的 `peers <= 0` 或 `capacity <= 0` |
| `ErrSenderOutOfRange` | `Sender < 0` 或 `Sender >= peers` |
| `ErrInvalidVector` | 向量为 `nil`、长度不等于 `peers`、含负值，或自身分量 `Vector[Sender] < 1` |
| `ErrBufferFull` | 消息暂不能交付且 `pending` 已达容量 |

所有拒绝都发生在写入状态之前；被拒绝后 `vc`、`pending`、交付序列、重复计数
均与调用前完全一致（测试 `TestInvalidInputs` / `TestBufferFull` 逐项快照校验）。
已经满足交付条件的消息不受缓冲容量限制。

判定顺序：发送方范围 → 向量合法性 → 重复 → 因果交付 → 缓冲满 / 入队。

## 并发语义

`Receive` 内部以单一互斥锁串行化判定与状态变更，可被任意 goroutine 并发调用：

- 一个因果闭合的消息集合（每条消息的向量依赖都在集合内）即使乱序并发到达，
  最终也会全部交付、每条恰好一次、缓冲清空、无重复计数，且交付序是合法因果线性化；
- 同一条消息被并发投递时，恰好一个调用得到 `OutcomeDelivered`，其余得到
  `OutcomeDuplicate`；
- 相同输入序列在全新缓冲上反复计算，每次结果与最终交付序列逐字节一致。

## 用法

```go
import "ontology/causalbuf"

b, err := causalbuf.New(3 /*peers*/, 100 /*capacity*/, causalbuf.WithLogger(logger))
r, err := b.Receive(causalbuf.Message{
    Sender: 0,
    Vector: []int{1, 0, 0},
    Payload: "A1",
})
// r.Outcome: OutcomeDelivered / OutcomeBuffered / OutcomeDuplicate
// r.Delivered: 本次调用交付的消息（含级联），按交付顺序
b.Vector()      // 本地向量快照
b.Delivered()   // 累计交付序列快照
b.DupCount()    // 重复计数
```

配置 `WithLogger`（任何带 `Printf` 方法的日志器，如标准库 `log.Logger`）后，
每次接收都会打印输入、判定依据（缺口 / 等待的因果前驱 / 重复来源 / 拒绝原因）
与交付结果。

## 本地验证

```bash
# 全量测试（含并发用例，-race 检测数据竞争）
go test -race -v ./causalbuf/

# 整个模块
go test -race ./...

# 覆盖率
go test -coverprofile=coverage.out ./causalbuf/
go tool cover -html=coverage.out

# 静态检查
gofmt -l .
go vet ./...

# 场景演示：打印输入、判定依据与交付结果
go run ./cmd/vcdemo
```

测试覆盖：乱序到达、级联交付与按发送方排序、因果前驱阻塞、重复丢弃
（已交付 / 缓冲中 / 并发重复）、构造参数与发送方及向量各类非法输入、
缓冲满且状态不变、并发闭合集合因果线性化、同序列重放确定性、日志内容。
