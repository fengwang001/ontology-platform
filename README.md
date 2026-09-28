# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 因果交付缓冲（`causal` 包）

`causal.Buffer` 基于向量时钟，把乱序甚至重复到达的广播消息按因果顺序
交付给上层：暂时不能交付的先缓冲，待前驱到齐后级联放出，使交付既不
超前也不遗漏。组件内部用互斥锁串行化"判定 + 状态变更"，`Receive`
可被多 goroutine 并发调用。

### 核心概念

- 进程数 `n`：参与广播的发送方总数，编号为 `0..n-1`。
- 消息：`Message{Sender, Vector, Payload}`，其中 `Vector` 长度为 `n`，
  `Vector[s]`（即本消息在发送方流中的序号，记为 `seq`）必须 `>= 1`。
- 本地向量时钟 `clock`：`clock[i]` 表示已交付的进程 `i` 的最大序号，
  初始全 0。

### 交付条件

一条来自发送方 `s` 的消息 `m` 在当前 `clock` 下可交付，当且仅当：

1. **发送方序号恰好接上**：`m.Vector[s] == clock[s] + 1`
   （有缺口则等待，超前的旧序号视为重复）；
2. **其余分量均不超过本地向量**：对所有 `i != s`，
   `m.Vector[i] <= clock[i]`
   （保证它所声明的因果前驱都已交付，不超前交付）。

满足后交付：对每个分量取 `max(clock[i], m.Vector[i])` 推进本地向量，
并把消息追加到全局交付序列。

### 级联交付

一次 `Receive` 交付首条消息后，反复扫描缓冲：每一轮在**所有当前可交付
的缓冲消息中取发送方编号最小者**交付，然后重新扫描，直到没有可交付
消息。同一发送方同一时刻至多一条消息满足"恰好接上"，因此该规则在任意
到达顺序下都给出唯一、确定的交付序（并发消息间按发送方编号打破平局，
不违反任何因果约束）。

### 重复判定（不是错误）

- `seq <= clock[s]`：该发送方序号已经交付过 → 丢弃，重复计数 +1，
  返回 `Duplicate: true`、`error = nil`；
- 消息尚未交付，但缓冲中已存在同一 `(sender, seq)` → 丢弃，计数 +1，
  缓冲中仍只保留一份（之后级联时只交付一次）。

### 拒绝原因（可区分、零副作用）

参数校验发生在任何状态变更之前；被拒绝的操作不改变本地向量、缓冲、
交付序列或重复计数。错误为 `*causal.RejectError`，其 `Reason` 取值：

| Reason | 触发条件 |
| --- | --- |
| `invalid_argument` | 消息为 `nil` 等参数非法 |
| `sender_out_of_range` | `Sender < 0` 或 `>= n` |
| `invalid_vector` | 向量长度不等于 `n`、存在负分量、或发送方分量 `< 1` |
| `buffer_full` | 消息需缓冲但待交付数已达 `capacity`；可立即交付的消息不受此限 |

构造参数非法（`n < 1`、`capacity < 0`）时 `New` 直接返回错误。

### 日志

默认向 stderr 打印每次接收的输入、判定依据（`gap`/`ahead`）、缓冲、
交付与级联结果、重复丢弃原因；可用 `NewWithLogger(n, cap, w)` 自定义
输出（传 `nil` 关闭日志）。

### 本地验证

```bash
# 全量测试（含并发与非法输入用例）
go test -v ./causal/

# 竞态检测 + 覆盖率
go test -race -coverprofile=coverage.out ./causal/
go tool cover -func=coverage.out
```

测试覆盖：顺序交付、乱序到达先缓冲后释放、多级级联、级联按最小编号
平局仲裁、已交付/已缓冲两类重复丢弃、四类非法输入拒绝后状态不变、
缓冲满拒绝、同一序列重复计算结果一致、因果闭合集合乱序并发到达后全部
恰好交付一次（50 轮随机排列）、并发重复精确计数。

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
