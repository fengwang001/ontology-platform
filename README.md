# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

本仓库当前提供 `ontology` 包：一个**分布式逻辑时钟（Lamport Clock）与事件全序/因果判定组件**，
能为多个节点上的本地、发送、接收三类事件打时间戳，并维护全部事件的全序与因果先后关系。

## 环境要求

- Go 1.26+（`go version` 确认）

## 模型与规则

### 1. 时钟推进

设节点 `i` 当前的标量 Lamport 时钟为 `C_i`（初值 0）：

| 操作 | 规则 | 事件时间戳 |
| --- | --- | --- |
| 本地事件 `Local(i)` | `C_i = C_i + 1` | 新的 `C_i` |
| 发送事件 `Send(i, m)` | `C_i = C_i + 1`，并把 `C_i` 随消息 `m` 登记 | 新的 `C_i` |
| 接收事件 `Receive(j, m)` | `C_j = max(C_j, t_send(m)) + 1` | 新的 `C_j` |

其中 `t_send(m)` 是消息 `m` 发送时携带的时间戳。接收取**较大者再加一**，
保证接收事件的时间戳严格大于发送事件。

除标量时钟外，每个事件还记录一份**向量时钟** `V`（仅用于因果判定）：
本地/发送时本节点对角分量 `V[i][i]++`；接收时先逐分量 `V[j][k] = max(V[j][k], V_send[m][k])`，
再 `V[j][j]++`。标量 Lamport 时间戳与向量时钟分开维护。

### 2. 全序（total order）

全部事件按 `(时间戳升序, 节点编号升序)` 排序：

```
e <ₜ f  ⇔  e.Clock < f.Clock，或 e.Clock == f.Clock 且 e.Node < f.Node
```

- 只取决于事件自身的 `(Clock, Node)`，**与事件被接受（执行/到达）的先后无关**；
- 同一节点上时间戳严格递增，因此不同事件不会在两个键上都相同；
- 该全序是因果偏序的一个一致扩展（见下），不会把因果先后排反。

### 3. 因果先后（happens-before）

因果边有两类，再取传递闭包：

1. **同一节点先后**：同节点序号小的事件先于序号大的事件；
2. **发送 → 接收**：消息的发送事件先于其接收事件。

判定直接比较两事件的向量时钟 `V_a、V_b`：

- `V_a[k] ≤ V_b[k]` 对所有 `k` 成立，且至少一个分量严格小 ⇒ `a` 先于 `b`（Before）；
- 反向成立 ⇒ `a` 后于 `b`（After）；
- 两者各有分量更大（不可比）⇒ **并发**（Concurrent）；
- 同一引用 ⇒ Equal。

向量时钟在接收时合并发送方时钟，因此它恰好编码了上述两类边的传递闭包。

### 4. 被拒绝的操作（零副作用）

下列操作会被拒绝，返回带可区分 `ErrorKind` 的 `*ClockError`，
且**不改变任何节点时钟、事件编号、全序或消息登记**（所有校验在状态修改之前完成）：

| ErrorKind | 触发条件 |
| --- | --- |
| `node_out_of_range` | 节点编号 `< 0` 或 `>= 节点总数` |
| `message_not_found` | 接收一条从未发送（登记）过的消息 |
| `duplicate_receive` | 同一条消息被重复接收（换节点也不行） |
| `duplicate_send` | 同一消息 ID 被重复发送 |
| `invalid_message_id` | 消息 ID 为空 |
| `too_many_events` | 已接受事件数达到构造时给定的上限 |
| `invalid_node_count` / `invalid_event_limit` | 构造参数 `<= 0` |
| `unknown_event` | 查询/比较引用了不存在的事件 |

### 5. 并发与确定性

- 所有方法均可用多 goroutine 并发调用，内部以读写锁保护；
- 并发下每个节点的事件编号仍从 1 开始**连续不间断**，时钟单调；
- 时间戳/向量只取决于事件的因果历史，与 goroutine 交错无关，因此
  **并发执行的全序与批量顺序重排一致，同一输入序列反复计算得到完全相同的输出**。

## 主要 API

```go
s, _ := ontology.NewSystem(3, 1000)        // 3 个节点，最多 1000 个事件
e1, _ := s.Local(0)                         // 本地事件
e2, _ := s.Send(0, "m")                     // 发送，登记消息 "m"
e3, _ := s.Receive(1, "m")                  // 接收，max(本地,发送时间)+1

s.Clocks()                                  // 各节点当前时钟
s.TotalOrder()                              // (时间戳,节点) 全序
s.Compare(ontology.EventRef{0, e2.Seq},
          ontology.EventRef{1, e3.Seq})     // Before/After/Concurrent/Equal
s.HappensBefore(a, b)
s.AreConcurrent(a, b)
detail, ok := s.IsCausalConsistent()        // 自检：时钟规则 + 全序因果一致
```

## 本地验证

```bash
# 全量测试
go test ./...

# 带竞态检测与详细日志（日志打印输入、时间戳、全序与判定依据）
go test -race -v ./...

# 只跑某类用例
go test -run 'ReceiveTakesMax|TotalOrder' ./ontology/   # 接收取较大值、并列排序
go test -run 'Causal' ./ontology/                       # 因果与并发判定
go test -run 'Invalid|Duplicate|TooMany|OutOfRange' ./ontology/  # 非法输入
go test -run 'Concurrent' -race ./ontology/             # 并发安全/确定性

# 可运行的用法示例（其输出由 go test 校验）
go test -run Example -v ./ontology/

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 代码检查
gofmt -l .
go vet ./...
```

## 目录

- `ontology/types.go`：事件/关系类型与可区分错误定义
- `ontology/system.go`：系统状态、时钟推进、事件与消息登记
- `ontology/order.go`：全序排序、向量时钟因果判定、因果一致性自检
- `ontology/*_test.go`：时钟推进、全序、因果、非法输入、并发与示例测试
