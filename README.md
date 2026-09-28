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

## 事务重组器（`txreassemble` 包）

把交错到达的变更事务事件按事务边界重组：已提交事务的完整行集整体输出，
回滚事务的行全部丢弃，使下游以事务为单位原子地收到数据。

### 事务生命周期

每个事务经历 `Begin → Write* → Commit | Rollback`：

1. `Begin(txID)`：事务进入进行中集合。
2. `Write(txID, row)`：行按到达顺序追加到该事务自己的缓冲区；
   多个事务可以交错写入，行分别缓冲、互不混杂。
3. 结束事务，二选一：
   - `Commit(txID)`：立即按行的到达顺序输出整个事务（**空事务也输出**），
     事务结束并释放缓冲。
   - `Rollback(txID)`：丢弃全部缓冲行，不产生任何输出，事务结束并释放缓冲。

### 输出顺序与确定性

- 提交是唯一的输出时刻：`Commit` 返回的 `CommittedTx` 即整体输出，
  同时也记录在 `Output()` 序列中。
- **输出顺序严格等于提交的到达顺序**（不是开始顺序），`CommitSeq` 从 1 起编号；
  每个事务内行的顺序等于写入到达顺序。
- 回滚事务的任何行都不会出现在输出中。
- 同一输入事件序列反复计算得到完全相同的输出（`TestDeterministicReplay` 覆盖）。
- 所有进行中事务缓冲的总行数有界，由 `New(maxBufferedRows)` 设定，
  按全部进行中事务的总行数计；超限的写入被拒绝且不入缓冲。

### 拒绝原因（可区分）

被拒绝的操作不会改变事务状态、缓冲或已输出序列，错误为 `*RejectError`：

| `RejectKind` | 触发条件 |
| --- | --- |
| `RejectInvalidEvent` | 空事务 ID、写空行、未知事件类型（含 `EventUnknown`） |
| `RejectDuplicateBegin` | 对已在进行中的事务再次 `Begin` |
| `RejectTxNotActive` | 对不在进行中的事务 `Write` / `Commit` / `Rollback` |
| `RejectBufferFull` | 写入会使总缓冲行数超过上限 |

`Commit` 与 `Rollback` 可并发调用，全部方法并发安全。
通过 `WithLogger` 挂接日志器后，每条输入事件都会打印输入、判定依据
（`accept` / `reject:<reason>`）与输出结果。

### 用法示例

```go
r := txreassemble.New(10_000).WithLogger(log.New(os.Stderr, "", log.LstdFlags))

r.Begin("A")
r.Begin("B")
r.Write("A", txreassemble.Row{Key: "a1"})
r.Write("B", txreassemble.Row{Key: "b1"})
r.Rollback("B")            // b1 被丢弃
tx, _ := r.Commit("A")     // 整体输出：[a1]，空事务也会输出
// r.Output() => [{TxID:"A", CommitSeq:1, Rows:[a1]}]
```

### 本地验证

```bash
# 格式化与静态检查（应无输出）
gofmt -l ./txreassemble
go vet ./...

# 全量用例（事务交错 / 回滚丢弃 / 空事务 / 缓冲满 / 非法输入 / 并发 / 日志）
go test -race -v ./txreassemble

# 反复运行确认并发稳定性与确定性
go test -race -count=10 ./txreassemble

# 查看日志中打印的输入、输出与判定依据
go test -race -v -run TestLogging ./txreassemble

# 覆盖率
go test -coverprofile=coverage.out ./txreassemble
go tool cover -func=coverage.out
```

