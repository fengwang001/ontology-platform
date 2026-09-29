# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 链式复制协调器（`chainreplication` 包）

`chainreplication` 实现链式复制：写从链头逐节点传到链尾，由链尾提交并应答；
读取只由链尾提供。网络通过注入的 `Network` 接口投递，可延迟、乱序、重复。

### 序号与提交规则

- 写只能发给当前链头；链头从 1 起连续分配序号（1, 2, 3, ...）。
- 节点严格按序号顺序应用：序号大于“已应用最大序号”的写先暂存，空洞补齐后依次应用；
  已应用或已暂存的重复消息直接丢弃。
- 任意时刻，靠后节点已应用的序号集合都是其每个前驱节点已应用集合的前缀。
- 写在链尾应用即**提交**：链尾回 ACK，ACK 沿链逆传；每个节点收到 ACK 后
  清除自身所有序号不大于该 ACK 的待确认写。
- ACK 到达链头时该写得到一次 `COMMITTED` 结果；每个写恰好得到一次
  `COMMITTED` 或 `UNCOMMITTED`。
- 读只能发给当前链尾，且只返回链尾已应用（即已提交）的最新值，
  因此读永远看不到未提交的写。
- 只剩一个存活节点时，它同时担任链头与链尾；本地应用即提交。

### 失效重组与补发规则

宣告节点失效时，该节点发出的以及所有与它相关的在途/排队消息全部丢弃，
被拒绝的宣告不改变任何状态。

- **链尾失效**：前驱成为新链尾，立即提交其全部“已应用但未确认”的写，
  ACK 随后正常逆传，已应答的写不丢失。
- **中间节点失效**：其前驱向新后继**恰好补发缺口**——只补发序号大于
  “新后继已应用最大序号”、且不大于前驱自身已应用最大序号的写；
  后继已应用的重复写会被丢弃。ACK 边界若被打断，会由补发通道重新弥合。
- **链头失效**：后继成为新链头；新链头尚未应用的写判定为 `UNCOMMITTED`
  （它们不可能再到达任何链尾），各存活节点暂存的对应高序号消息被清除，
  新写在新链头已应用前缀之上无空洞地继续编号。新链头已经应用的写仍会
  通过正常链路在链尾提交。

### 拒绝原因（`RejectReason`，可区分）

| 原因 | 触发场景 |
| --- | --- |
| `empty_chain` | 构造时空链 |
| `empty_node_id` | 构造时存在空节点标识 |
| `duplicate_node_id` | 构造时节点标识重复 |
| `not_head` | 向非链头节点写 |
| `not_tail` | 向非链尾节点读 |
| `unknown_node` | 宣告/操作不存在的节点 |
| `node_already_failed` | 操作已失效节点，或重复宣告失效 |
| `last_survivor` | 宣告最后一个存活节点失效 |

### 并发与确定性

写、读、`Deliver` 投递与 `Fail` 失效宣告均可并发调用，所有状态在单一互斥锁下变更。
相同的操作与投递顺序重放，结果（含日志）完全相同。注入
`chainreplication.DefaultLogger()` 可在日志中看到每次输入、输出与判定依据。

### 使用示例

```go
logger := chainreplication.DefaultLogger()
net := chainreplication.NewTestNetwork(logger) // 消息先排队，由测试显式投递
c, err := chainreplication.NewCoordinator([]string{"h", "m", "t"}, net, logger)

h, err := c.Write("h", "value-1") // 只在链头接收，返回序号与结果通道
net.DeliverAll(c)                 // 推动写与 ACK 沿链传播
committed := <-h.Outcome()        // true=已提交，false=未提交，恰好一次

value, seq, ok, err := c.Read("t") // 只在链尾读取已提交值
_ = c.Fail("m")                    // 中间节点失效，前驱恰好补发缺口
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 编译全部包
go build ./...
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./chainreplication
go test -run TestMiddleFailureRetransmitsExactGap ./chainreplication

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

`chainreplication` 覆盖的用例包括：三种构造期拒绝与全部运行期拒绝原因、
单节点兼任头尾、乱序与重复投递（空洞暂存/补齐/去重）、读不见未提交写、
中间节点失效时恰好补发序号缺口、链尾失效后新链尾立即提交、链头失效后
未提交判定与序号续写、并发写入/投递/读/失效下的前缀关系与竞态检测、
以及相同脚本重放结果完全一致。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
