# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## MV2PL 认证锁管理器

`mv2pl.go` 实现多版本两阶段锁（Multiversion 2PL）认证锁管理器：读者只读已提交值，写者先在私有缓冲上持 X 锁，提交时按键升序把 X 转为与一切冲突的 C（认证）锁并等待读者退出。

### API

- `NewManager(K int) (*Manager, error)`：`K` 必须在 `1..64`，否则整体拒绝并返回 `ErrInvalidK`。
- `Begin() Result`：返回从 1 起递增的事务号。
- `Read(t, k) Result` / `Write(t, k, x) Result` / `Commit(t) Result` / `Abort(t) Result`。
- 每个 `Result` 含本次调用自身的结果（`Value`/`Status`/`Deadlock`/`RejectErr`）与按发生次序排列的 `Events`；授予事件携带读到的值，提交事件每键一个（只读提交 `Key == -1`）。

### 相容矩阵（仅不同事务之间）

| 请求 \ 已授予 | S | X | C |
| --- | --- | --- | --- |
| **S** | 相容 | 相容（读已提交旧值） | 冲突 |
| **X** | 相容 | 冲突 | 冲突 |
| **C** | 冲突 | 冲突 | 冲突 |

同一事务自己的锁永不与自己冲突。

### 普通申请与授予

- `Read`：已持 X 返回缓冲值（不加新锁）；已持 S 返回已提交值；否则申请 S。
- `Write`：已持 X 覆盖缓冲；持有 S 不算升级，按普通申请重新申请 X。
- 普通申请**当且仅当该键队列为空且与所有他事务已授予锁相容**时立即授予；否则追加队尾，事务转等待态。

### 转换队列位置

提交时按键升序处理每个 X 键：

- C 与他事务已授予锁相容则**立即授予（不看队列）**，取代该键上自己的 X；
- 否则插入该键队列中**最后一个 C 转换请求之后、全部普通请求之前**，事务转提交中态。

因此转换不会被后来的普通读者/写者饿死；两个转换之间严格按转换入队次序。两个不同事务的 C 在同一键同时排队在本协议下必然成环（后入的 C 经前序边指向前一个 C，而前一个 C 等待后者的 X），这正是“若排到队尾则死锁”的反例情形。

### 严格先进先出的处理次序

每次释放（提交或中止）后维护待处理键集合，反复取**最小编号**的键，从队首起逐个授予相容请求，遇到第一个不相容的请求即停。授予引发的提交/释放把新键并入同一集合，随后重新从最小键开始。多键提交的提交事件按键升序产生。

### 死锁边的定义

申请每入队一次就做一次判定（`Commit` 逐键入队则逐键判定）。对当前全部队列条目构造 waits-for 图：

- 每个条目指向：与其冲突的**他事务已授予锁持有者**；以及队列中排在它前面的**所有他事务条目**；
- 持有者节点再跳到该事务自己的所有队列条目。

若请求者沿这些边能回到自己，则**只中止请求者**：移除其全部队列条目、释放其全部锁、丢弃缓冲；其余事务不受影响。后续键不再申请。`Abort` 处理相同但不作死锁判定，其释放引发的授予/提交事件照常按序返回。

### 拒绝次序

被拒绝的调用不改变任何状态。`Read`/`Write`/`Commit` 按以下顺序只报第一个原因：

1. 事务号不存在（`ErrUnknownTxn`）；
2. 状态不符：发起调用须活跃（`ErrTxnNotActive`）；`Abort` 须活跃、等待或提交中（`ErrTxnNotAbortable`）；
3. 键越界（仅 `Read`/`Write`，`ErrKeyOutOfRange`）。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细事件日志（每个关键场景打印输入、输出与判定依据）
go test -race -v ./...

# 仅跑 2000 组随机序列与独立朴素模拟的差分对照
go test -run TestDifferentialRandom -v .

go vet ./...
gofmt -l .
```

`mv2pl_spec_test.go` 覆盖：转换优先于普通写、新读者不得越过排队的 C、转换位于先到转换之后、互读对方将写键的提交死锁、经队列前序条目形成的环、死锁中止后按键升序级联、提交中途 C 继续持有、提交中被 Abort 释放全部锁、读自己缓冲不加锁。`naive_sim_test.go` 与 `diff_driver_test.go` 用独立朴素实现对 2000 组随机调用序列逐步对照结果与事件序列。

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
