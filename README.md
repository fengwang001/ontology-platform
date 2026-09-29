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

## 链式复制协调器（chain replication）

`chainreplication` 包实现链式复制协调器：写只从链头进入，逐节点传到链尾，链尾提交并应答；读只由链尾提供已提交数据。代码位于 `chainreplication/`。

### 序号与提交规则

- 写只由链头接收，序号从 1 起全局连续分配（头失效后也不复用已未决的号）。
- 节点严格按序号顺序应用：未来序号先暂存（形成空洞），重复投递（已应用或已暂存）直接丢弃。
- 链尾"应用即提交"，返回覆盖其整个已应用前缀的累计 ACK；ACK 逆传，经过节点时清除该节点不大于 ACK 的待确认写。
- 累计 ACK 到达链头后，覆盖到的每个写得到唯一一次 `committed` 结果（`WithResultCallback`）。
- 链尾读只暴露连续的已提交前缀，任何未提交（在途/暂存）的写都不可见。
- 只剩一个节点时它兼任头尾：写应用即同步提交，无网络跳。

### 三种失效的重组与补发

`Fail(id)` 先通过注入网络的 `FailNode` 丢弃失效节点发出的与所有在途触及它的消息，再按位置重组：

- **链尾失效**：前驱成为新链尾，立即提交其持有的全部待确认写，并发累计 ACK 上行；若只剩一个存活者则兼任头尾。
- **中间节点失效**：前驱直连失效节点的后继，恰好补发「序号大于新后继已应用最大序号、且不超过自身已应用最大序号」的缺口，不重复补发。
- **链头失效**：后继成为新链头；新链头尚未应用的写一次性报告为未提交（每个写恰好一次），因空洞滞留在新链头的孤儿值被丢弃；新链头再把超过后继水位但自己持有的写补发下去。若存活链尾此前已提交更多写、只是 ACK 随旧链头丢失，链尾会重发累计 ACK，已应答的写不丢。

### 拒绝原因（均不改状态，哨兵错误可区分）

- 构造时空链 `ErrEmptyChain`、节点标识为空 `ErrEmptyNodeID`、标识重复 `ErrDuplicateNodeID`。
- 向非链头写 `ErrWriteNotAtHead`；向非链尾读 `ErrReadNotAtTail`。
- 宣告未知节点 `ErrUnknownNode`、已失效节点 `ErrNodeFailed`、最后一个存活节点失效 `ErrLastNodeAlive`。

### 并发与确定性

`Write`/`Read`/Deliver/`Fail` 可并发调用（单把锁串行化协议状态）。任意时刻靠后节点的已应用序号集合都是靠前节点的前缀；每个已接受的写恰好得到一次提交或未提交结果；相同操作与投递脚本重放结果完全一致。日志对每次输入、输出与判定依据打印 `INPUT`/`OUTPUT`/`DECIDE`/`REJECT`/`DISCARD`。

### 本地验证

```bash
# 全量测试（建议始终带竞态检测）
go test -race ./chainreplication/ -v

# 只跑关键场景
go test -race -run 'TestMiddleFailure|TestTailFailure|TestHeadFailure|TestReplayDeterminism|TestConcurrentWrites' ./chainreplication/ -v

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
```

若默认的 Go 构建缓存目录只读，可指定可写缓存，例如 `GOCACHE=/tmp/gocache`。

测试场景与需求对应关系：

- `TestMiddleFailureResendsExactlyGap`：中间节点失效恰好补发缺口。
- `TestTailFailureCommitsImmediately` / `TestTailFailureDownToSingleNode`：尾失效后立即提交。
- `TestHeadFailureUncommitted` / `TestHeadFailureAckRecovered` / `TestHeadFailureBufferedHoleDropped`：头失效的未提交判定、已应答不丢、孤儿空洞清理。
- `TestOutOfOrderBufferingAndDuplicates`：乱序暂存与重复丢弃。
- `TestReadNeverReturnsUncommitted` / `TestBasicFlowCommitAndRead`：读不见未提交写。
- `TestConcurrentWritesPrefixInvariant`：并发写入下的前缀关系（`-race`）。
- `TestReplayDeterminism`：相同操作与投递序列重放结果一致。
- `TestOperationRejectionsDoNotChangeState` / `TestConstructionRejections`：拒绝原因可区分且不改状态。
