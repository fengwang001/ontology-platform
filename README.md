# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 元数据日志（`journal` 包）

`journal` 实现一个带撤销记录、检查点与盘上版本比较的追加式元数据日志，可在任意位置断电后按前缀回放出磁盘映像。

### 记录类型

日志是追加的记录序列，事务号从 1 起连续，当前未提交事务的号为已提交数加 1。记录共四种：

- `Write(tid, block, payload)`：追加到当前事务，块号为非负整数，payload 0–1024 字节（空载荷合法，且与「从未写过」不同），追加时拷贝。
- `Revoke(tid, block)`：追加到当前事务，声明撤销该块在此事务之前的写入。
- `Commit(tid)`：提交当前事务并追加一条 Commit 记录；空事务（没有任何 Write/Revoke）拒绝提交。
- `Checkpoint(upto)`：声明事务号不大于 upto 的事务已写回磁盘，立即追加，可出现在未提交事务的记录之间；`upto` 须不小于上一条 Checkpoint 的 upto（初始为 0）且不大于已提交事务数。

`Commit` 与 `Checkpoint` 不计入构造参数 `Cap`（单个事务可容纳的 Write/Revoke 记录数上限，`Cap < 1` 整体拒绝）。`Records()` 返回含 Commit 与 Checkpoint 的总记录数。

### 撤销表与判定次序

`Recover(disk, n)` 只看日志前 `n` 条记录（模拟写完第 `n` 条后断电），返回新映像而不改动 `disk` 与日志：

1. 只有其 `Commit` 记录落在前 `n` 条内的事务生效，其余事务的 Write/Revoke 整体忽略并计入 `Ignored`。
2. 前 `n` 条内最后一条 Checkpoint 的 upto 记为 `K`（没有则为 0）。
3. 建撤销表：对每个生效事务 `t` 与块 `b`，若 `t` 内关于 `b` 的最后一条记录是 Revoke，则 `t` 对 `b` 撤销生效；撤销表取 `b` 的最大这样的 `t`（检查点内的事务也参与建表）。
4. 按日志顺序处理生效事务中的每条 `Write(t, b, p)`，依次判定：
   - `t <= K` → 计入 `Checkpointed`，不写；
   - 否则撤销表中 `b` 的值 `>= t` → 计入 `Skipped`；
   - 否则 `disk` 中 `b` 的 ver（以传入的初始 disk 为准，不随回放改变；缺失块视为 ver 0）`>= t` → 计入 `Stale`；
   - 否则写入映像（该块 ver 记为 `t`，后写覆盖先写，同事务同块的多条 Write 都写入）并计入 `Applied`。

任一 `n` 下 `Applied + Skipped + Stale + Checkpointed` 等于前 `n` 条内已提交事务的 Write 记录总数。

### 前缀回放与并发

同一日志与同一 `n` 的回放结果确定；日志继续追加后原先某个 `n` 的回放结果不变；相同操作序列重放得到完全相同的日志。`Write`、`Revoke`、`Commit`、`Checkpoint`、`Recover` 与 `Records` 均可并发调用，结果等价于某个串行顺序。

### 拒绝规则

被拒绝的操作不改变日志与当前事务，按顺序只报第一个错误（均可 `errors.Is` 区分）：参数非法（`ErrInvalidArgument`：块号为负、payload 超过 1024 字节、`Recover` 的 `n` 越界、Checkpoint 的 upto 倒退或超过已提交数）→ 事务已满（`ErrTransactionFull`）→ 空事务（`ErrEmptyTransaction`）。

### 本地验证

```bash
# 单元测试 + 2000 组随机日志与朴素回放对照（-v 打印输入、输出与判定依据）
go test ./journal
go test -race -v ./journal

# 静态检查与格式
go vet ./...
gofmt -l .
```

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
