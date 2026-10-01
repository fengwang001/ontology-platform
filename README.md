# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## ARIES 分析阶段

核心实现位于 `aries` 包。`Calculator.Append` 原子追加日志记录，`Calculator.Analyze` 只读重建状态；同一个计算器可并发调用。也可以直接调用纯函数 `aries.Analyze(records)` 重放一组已有日志。

### 分析起点

- 找到最后一个拥有匹配 `EndCkpt` 的 `BeginCkpt`：`EndCkpt.begin` 必须指向已有 `BeginCkpt`，且该 `BeginCkpt` 只能完成一次。
- 未匹配 `EndCkpt` 的 `BeginCkpt` 视为未完成并忽略；此时回退到更早的完整检查点。
- 以所选 `EndCkpt` 的 `dpt` 和 `att` 快照初始化脏页表与活跃事务表。
- 从 LSN 严格大于对应 `BeginCkpt.LSN` 的记录继续处理；所有 `EndCkpt` 本身都不重放。
- 没有完整检查点时，两张表均从空状态开始扫描全部日志。

### 两张表的规则

- `Update(txn, page)`：事务不在活跃表中时以 `running` 加入，已存在时只更新最后 LSN，不改变状态；页不在脏页表中时以当前 LSN 作为 `recLSN` 加入，已存在时保持不变。
- `Commit(txn)` 将事务置为 `committed`，`Abort(txn)` 置为 `aborting`，并更新最后 LSN；事务原本不在活跃表时以对应状态加入。
- `End(txn)` 将事务从活跃表移除；不存在时无操作。
- `PageFlush(page)` 将页从脏页表移除；后续再次 `Update` 同一页时，会以新记录的 LSN 重新成为 `recLSN`。

### 结果与拒绝原因

`Analysis` 包含：

- `RedoLSN *int64`：脏页表中的最小 `recLSN`；脏页表为空时为 `nil`，表示无需重做。
- `DirtyPages []DirtyPage`：按页 ID 升序。
- `ActiveTransactions []ActiveTransaction`：按事务 ID 升序，包含状态与最后 LSN。
- `FailedTxns []string`：活跃表中状态为 `running` 或 `aborting` 的事务，按事务 ID 升序；`committed` 但尚未 `End` 的事务仍在活跃表中，但不属于失败事务。

追加按以下顺序报告第一个可区分错误；失败记录不会改变日志：

1. 记录字段或类型无效。
2. LSN 不大于已有最大 LSN：`ErrNonIncreasingLSN`。
3. `EndCkpt.begin` 不指向已有 `BeginCkpt`：`ErrBeginCheckpointNotFound`。
4. 对应 `BeginCkpt` 已有 `EndCkpt`：`ErrCheckpointAlreadyCompleted`。
5. 记录引用已 `End` 的事务（含检查点 ATT 快照）：`ErrEndedTransaction`。
6. `Commit`、`Abort` 或 `End` 引用从未出现的事务：`ErrUnknownTransaction`。检查点 ATT 快照中的事务视为已出现。

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

# 仅验证 ARIES 分析阶段（含 1000 组随机日志对拍）
go test -race ./aries

# 查看某一组随机日志的输入、输出与检查点选择依据
go test -v -run 'TestAnalyzeRandomLogsAgainstNaiveReplay/seed-1$' ./aries

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
