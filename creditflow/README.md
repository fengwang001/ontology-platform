# creditflow：基于信用的点对点流控

`creditflow` 包在**一个发送端**与**一个接收端**之间实现按信用（credit）计数的
流控：接收端按接收缓冲空位通告信用，发送端只在持有信用时发送，并把暂时发不出
的消息留在积压队列中，待信用到达后按顺序自动补发。

## 核心概念

- `capacity`：接收端缓冲容量（消息条数）。
- `backlogLimit`：发送端积压上限；超过即拒绝生产。
- `credit`：发送端当前持有的可用信用，任意时刻非负。
- `inFlight`：已授予、但尚未被一次实际投递核销的信用。
- `buffered`：已送达接收端、尚未被消费的消息数。
- 编号：消息按生产顺序编号，从 `1` 开始，永不复用。

## 规则

### 信用与自动发送

- 生产（`Produce`）的消息**先进入积压**，随后在同一临界区内自动尝试发送：
  只要 `credit > 0` 且积压非空，就**先扣 1 个信用、再发送 1 条**，循环直至
  信用为零或积压清空。
- 因此信用永远不会变负；信用为零时一条都不发。
- 通告（`Advertise`）与探测（`Probe`）授予信用后同样立即触发上述自动补发。

### 通告（Advertise）

- 可授予信用 = `capacity - buffered - inFlight`（即真实空位）。
- 结果为正：授予对应数量的信用；结果非正：**什么都不做**（不授予、不记忆）。
- 通告不消费任何消息。

### 消费（Consume）

- 消费严格按编号顺序进行：只接受“下一个应消费编号”，重复、跳号、未来编号、
  尚未送达的编号一律拒绝（`ErrConsumeOutOfBounds`）。
- 消费**不触发通告**，不授予任何信用。

### 探测（Probe）

- 仅当 `credit == 0` **且**积压非空时允许；否则返回 `ErrProbeNotAllowed`。
- 允许时效果与一次 `Advertise` 完全相同：按真实空位授予信用并补发。
- **不保底、不记忆**：空位为零时授予 0；探测本身不留状态，之后不会补放。

### 不变量

- 任意时刻 `buffered + credit <= capacity`，且 `credit >= 0`。
- `producedTotal == backlog + buffered + consumedTotal`（总数守恒）。
- 消费编号连续、无重复、无跳号，同一脚本操作结果可复现。
- 发送侧与接收侧方法可由不同 goroutine 并发调用，内部以单一互斥锁串行化。

## 边界与错误类别

四类错误互不相同、可用 `errors.Is` 区分；**任何被拒调用都不改变任何状态**
（失败不留痕）：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidParam` | `capacity` 或 `backlogLimit` 非正 |
| `ErrBacklogOverflow` | 生产会使积压超过 `backlogLimit` |
| `ErrConsumeOutOfBounds` | 消费编号非正、重复、跳号、未来编号或尚未送达 |
| `ErrProbeNotAllowed` | 仍有信用，或积压为空时探测 |

## 日志

每一步均通过 `slog` 打印：操作名与输入（如 `assigned_seq`、`requested_seq`）、
`credit`、`in_flight`、`backlog`、`buffered`、`produced_total`、`consumed_total`，
以及 `decision`（`accept` / `grant` / `noop` / `reject`）与 `reason` 判定依据。

## 本地验证

```bash
# 全部测试（含竞态检测）
go test -race -v ./creditflow

# 全量测试、代码检查与格式
go test ./...
go vet ./...
gofmt -l .

# 覆盖率
go test -coverprofile=coverage.out ./creditflow
go tool cover -html=coverage.out
```

测试覆盖：信用授予/扣减与积压变化、自动补发、通告非正不授予、消费顺序与
“不触发通告”、探测的允许/拒绝/不保底/不记忆、四类非法输入及拒绝后状态不变、
与逐条发送参照模型（`refModel`）逐步比对，以及三 goroutine 并发压测
（生产端 / 通告端 / 消费端，配合 `-race` 校验）。
