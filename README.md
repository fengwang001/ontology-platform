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

## 逻辑复制槽（`replication` 包）

`replication` 包实现一个带持久化的逻辑复制槽，维护**确认位点**与
**重启位点**，按需保留/回收日志，使崩溃重启后能重新发出尚未确认的事务，
做到**不重不漏**。

### 日志记录与事务解码

- 每条记录携带严格递增的 `LSN`（日志序号）、事务标识 `XID` 与类型
  `Begin / Data / Commit / Abort`；不同事务的记录可以交错。
- 解码器按 `Begin` 缓冲整个事务：
  - 读到 `Commit`：把该事务的全部记录**整体原子发出**；
  - 读到 `Abort`：丢弃缓冲，事务**永远不会发出**（其记录也不再保留）。

### 两个位点

- **确认位点 `confirmedLSN`**：消费端已确认的位置，必须**恰好**等于某个
  已发出事务的 `Commit` 位点。确认只能前进（相等时幂等、不改变状态）。
- **重启位点 `restartLSN`**：崩溃恢复时开始重放的位置，定义为
  `min(confirmedLSN, 所有进行中或已提交未确认事务的起点 LSN)`。
- 恒成立：`restartLSN <= confirmedLSN`；两个位点都只进不退，
  并发读取到的快照单调且前者不大于后者。

### 保留与回收

- **保留**：进行中事务的全部记录，以及已提交但未确认事务的全部记录
  （这是重启后重新发出事务所必需的）。
- **回收**：`LSN < restartLSN` 的日志前缀可安全回收；物理 WAL 通过
  “写临时文件 + rename + fsync 目录”压缩，保证原子切换。
- 落盘顺序为“**先持久化新位点，再物理回收**”：若崩溃发生在两步之间，
  恢复时最多多保留日志，绝不会丢失需要重发的数据。
- 中止事务在读到 `Abort` 时立即从保留集合剔除。

### 崩溃恢复

打开槽时重放保留的 WAL：提交位点 `<= confirmedLSN` 的事务视为已确认、
不再发出；其余已提交事务按提交位点升序作为**待重发事务**
（`Slot.Reemitted()`），保证每个已提交未确认事务重启后恰好再发一次。
WAL 尾部的不完整帧（崩溃撕裂）被忽略；帧内 CRC/魔数错误返回
`ErrCorrupted`。

### 拒绝的操作（错误原因可区分，且不改变任何状态）

| 情形 | 错误 |
| --- | --- |
| 记录类型非法、`XID=0`、LSN 未严格递增、未 Begin 即 Data/Commit/Abort、重复 Begin | `ErrInvalidRecord` |
| 确认位点不在任何已发出事务的提交边界上 | `ErrInvalidConfirm` |
| 确认位点小于当前确认位点（回退） | `ErrConfirmRewound` |
| 进行中事务数超过上限（`WithMaxInProgress`，默认 1024） | `ErrTooManyInProgress` |

被拒绝的 `Append` / `Confirm` 不会落盘，也不改变日志、回收位置、
解码状态或两个位点。

### 并发与确定性

`Append`、`Confirm` 与位点读取可并发调用，内部由互斥锁串行化状态迁移；
追加在“校验通过后写 WAL 并 `fsync`”成功后才推进内存状态。纯函数
`replication.Replay(records, confirmedLSN, maxInProgress)` 对同一输入序列
反复计算得到完全相同的输出。

### 日志

槽以 JSON 行输出结构化日志（`WithLogger` 可重定向），每次追加/确认都记录
**输入记录、确认位点、重启位点、发出的事务与判定依据**；被拒绝的操作输出
`append_rejected` / `confirm_rejected` 事件。

### 本地验证

```bash
# 全量测试（带竞态检测，建议重复运行观察并发稳定性）
go test -race -count=3 ./replication/

# 详细输出，可查看 JSON 结构化日志内容
go test -race -v ./replication/

# 覆盖率
go test -coverprofile=coverage.out ./replication/
go tool cover -html=coverage.out
```
