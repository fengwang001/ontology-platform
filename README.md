# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## recovery：ARIES 恢复分析阶段计算器

`recovery` 包从追加的日志记录中重建脏页表（DPT）与活跃事务表（ATT），并给出重做起点与失败事务集合。

### 日志记录

LSN 严格递增，类型：`Update(txn, page)`、`Commit(txn)`、`Abort(txn)`、`End(txn)`、`BeginCkpt`、`EndCkpt(begin, dpt, att)`、`PageFlush(page)`。`EndCkpt` 的 `begin` 为对应 `BeginCkpt` 的 LSN，`dpt` 为页到 recLSN 的快照，`att` 为事务到（状态, 最后 LSN）的快照。

### 分析起点

取最后一个有对应 `EndCkpt` 的 `BeginCkpt`（没有 `EndCkpt` 的 `BeginCkpt` 视为未完成而忽略），以该 `EndCkpt` 的快照初始化两张表，再按 LSN 顺序处理 LSN 大于该 `BeginCkpt` 的其余记录（`EndCkpt` 本身不再处理）；没有完整检查点则从空表扫描全部日志。

### 两表更新规则

- `Update`：事务不在 ATT 则以运行态加入，已在则只更新最后 LSN 而不改状态；页不在 DPT 则加入且 recLSN 为本记录 LSN，已在则不变。
- `Commit` / `Abort`：置已提交 / 回滚中并更新最后 LSN（事务不在 ATT 则以对应状态加入）。
- `End`：从 ATT 移除（不在则无操作）。
- `PageFlush`：从 DPT 移除该页。

### 结果字段

- `NeedRedo` / `RedoLSN`：重做起点 = DPT 中最小 recLSN；DPT 为空则 `NeedRedo=false`（无需重做）。
- `DPT` / `ATT`：两张表均按键升序。
- `Failed`：ATT 中状态为运行或回滚中的事务，升序。

### Append 校验

以下情况整体拒绝且日志不变，按此顺序只报第一个原因：LSN 不大于已有最大 LSN（`ErrLSNNotIncreasing`）；`EndCkpt` 的 begin 不是已有 `BeginCkpt` 的 LSN（`ErrEndCkptUnknownBegin`）或该 `BeginCkpt` 已有 `EndCkpt`（`ErrEndCkptDuplicate`）；记录引用已 `End` 的事务（`ErrTxnAlreadyEnded`）；`Commit`/`Abort`/`End` 引用从未出现的事务（`ErrTxnNeverSeen`，检查点快照中的事务视为已出现）。

`Append` 与 `Analyze` 可并发调用（读写锁保证可串行化）；`Analyze` 不改变日志；相同追加序列重放得到完全相同的结果。

### 本地验证

```bash
# 全量测试（含 1000 组随机日志与朴素实现对拍）
go test ./recovery/

# 查看对拍用例的输入、输出与判定依据
go test -run TestDifferentialRandomLogs -v ./recovery/

# 竞态检测（并发 Append/Analyze）
go test -race ./recovery/
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
