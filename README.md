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

## 混合逻辑时钟（`hlc` 包）

`hlc` 为每个节点维护 `(logical, counter)` 组成的混合逻辑时钟，在物理时钟
可能回拨的情况下仍保证时间戳严格单调且因果一致：同一节点相邻事件严格递增，
接收事件严格大于其发送事件，每条消息只能被目标节点恰好接收一次。

### 三类事件的推进规则

记节点当前逻辑时间为 `l`、计数为 `c`，物理读数为 `pt`，消息携带 `(lm, cm)`：

- **本地 / 发送事件**：`l' = max(l, pt)`。若 `l' == l`（物理未推进或回拨），
  则 `c' = c + 1`；否则 `c' = 0`。
- **接收事件**：`l' = max(l, lm, pt)`。
  - `l' == l == lm`：`c' = max(c, cm) + 1`；
  - `l' == l`：`c' = c + 1`；
  - 消息时间占主导（`l' == lm`）：`c' = cm + 1`，保证接收严格大于发送。

时间戳按 `(logical, counter)` 字典序比较。物理回拨时逻辑时间不回退，
仅通过计数递增保持单调。

### 偏差与计数上限

- `Config.MaxClockOffset`：物理读数允许超前节点当前逻辑时间的最大值，
  超出即拒绝（节点尚无事件时不做偏差校验，因为没有参照基准）。
- `Config.MaxCounter`：计数器上限，需要继续自增而已达到上限时拒绝。

### 错误类别

所有非法输入整体拒绝、互不相同、可用 `errors.Is` 区分，且拒绝后节点时钟、
在途消息与事件历史均不变（失败不留痕）：

| 错误 | 含义 |
| --- | --- |
| `ErrEmptyNodeID` | 节点标识为空 |
| `ErrNodeExists` | 节点已存在 |
| `ErrNodeNotFound` | 节点不存在 |
| `ErrNegativePhysical` | 物理读数为负 |
| `ErrEmptyMessageID` | 消息标识为空 |
| `ErrMessageNotFound` | 消息号不存在 |
| `ErrMessageAlreadyReceived` | 消息已被接收 |
| `ErrNotRecipient` | 当前节点不是消息目标 |
| `ErrClockOffsetExceeded` | 物理读数偏差超限 |
| `ErrCounterOverflow` | 计数达到上限 |

### 并发与历史查询

`Network` 的全部操作由互斥锁串行化、可线性化，支持多执行体并发调用；
同一事件序列重复执行得到完全相同的时间戳。`History(nodeID)` 返回该节点
按时间戳升序排列的事件历史副本，`Now(nodeID)` 查询当前时间（不产生事件）。

### 本地验证

```bash
# 全部测试（含物理回拨、偏差/计数上限、非法输入、因果一致性、并发）
go test ./hlc/

# 查看每步输入、时间戳与判定依据的日志
go test ./hlc/ -v

# 竞态检测
go test -race ./hlc/
```
