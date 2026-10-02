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

## 调度器待调度队列（`schedqueue` 包）

`schedqueue` 实现了一个确定性的调度器待调度队列，每个存活 Pod 在任意时刻
恰好处于 **活跃（active）**、**退避（backoff）**、**不可调度（unschedulable）**、
**在途（in-flight）** 四个位置之一，四者成员数之和恒等于存活 Pod 数。

### 构造

```go
q, err := schedqueue.New(B, M, L) // 毫秒，要求 1 <= B <= M <= 1e12，1 <= L <= 1e12
```

- `B`：退避基数；`M`：退避上限；`L`：不可调度滞留上限。
- 构造参数越界时整体以 `RejectInvalidConfig` 拒绝。

### 队列流转规则

- `Add(id, prio, now)`：新 Pod 进入活跃队列，`t0 = now` 此后不变，`att = 0`。
- `Pop(now)`：先执行 `Advance(now)`，再从活跃队列弹出优先级最高者
  （`prio` 大者先；并列时 `t0` 小者先；再并列时 ID 字节序小者先），
  标记为在途、`att` 加 1 并记下当前事件计数 `seq`。活跃队列为空时成功但返回无 Pod。
- `Done(id, outcome, fb, now)`：仅针对在途 Pod。
  - `Scheduled`：删除该 Pod（`fb` 被忽略）。
  - `Failed`：记录 `fb`，计算退避到期时刻
    `exp = now + min(B·2^(att−1), M)`（乘法不溢出，超出 `M` 即封顶）。
    若 `Pop` 之后（事件序号大于记下的 `seq`）发生过与该 Pod 相关的事件，
    则进入退避队列，否则进入不可调度队列并记 `parked = now`。
- `Event(ev, now)`：事件计数加 1 并记录掩码 `ev`（1–255），然后把不可调度队列中
  与该事件相关的 Pod 搬走：`now < exp` 者进退避队列，否则进活跃队列；
  不相关的 Pod 留在不可调度队列。
- `Advance(now)`：把退避队列中 `exp <= now` 的 Pod 移入活跃队列，并把不可调度
  队列中 `parked + L <= now` 的 Pod（不论是否相关）按
  「`now < exp` 进退避队列、否则进活跃队列」搬走；不增加事件计数。
- `Remove(id)`：从任何位置（含在途）删除 Pod。
- `Sizes()`：不改状态，返回活跃、退避、不可调度、在途各自的成员数。

### 事件相关性

事件掩码 `ev` 与失败掩码 `fb` 相关，当且仅当 `fb == 0` 或 `fb & ev != 0`。
相关性同时作用于两处：`Done` 决定在途失败的 Pod 进退避还是不可调度队列
（只看 `Pop` 之后发生的事件，`Pop` 之前的事件不影响本次在途），以及
`Event` 决定不可调度队列中的哪些 Pod 被搬走。不可调度队列中的 Pod 自入队起，
除非发生相关事件或滞留满 `L`，否则不会离开。

### 拒绝语义

被拒绝的操作不改变任何队列、`att`、`fb` 与计数。拒绝原因可区分且只报第一个，
顺序为：参数非法（空 ID、`now < 0`、非法 outcome、`fb` 不在 0–255、`ev` 不在
1–255）→ 时钟回退（`now` 小于已被接受操作见过的最大 `now`，`Pop`、`Event`、
`Advance` 成功也计入）→ `Add` 的 ID 已存在 → `Done`/`Remove` 的 Pod 不存在 →
`Done` 的 Pod 存在但不在途。

### 并发与确定性

所有方法可并发调用（内部互斥锁串行化），结果等价于某个串行顺序；相同操作
序列重放得到完全相同的弹出序列与各队列成员。

### 本地验证

```bash
# 单元测试 + 2000 组随机操作序列与朴素模拟逐步对照
go test ./schedqueue/

# 打印每个随机操作的输入、输出与判定依据
go test -v -run TestRandomAgainstSim ./schedqueue/

# 竞态检测
go test -race ./schedqueue/
```
