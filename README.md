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

## 元数据日志

`ontology.MetadataLog` 是可并发追加的确定性事务日志。`NewMetadataLog(capacity)` 的 `capacity` 必须不小于 1，它只限制每个未提交事务中的 `Write` 与 `Revoke` 数量；`Commit` 与 `Checkpoint` 不计入容量。

### 记录类型

- `Write(tid, block, payload)`：把块载荷写入当前事务，`payload` 长度为 0 到 1024，追加时复制。
- `Revoke(tid, block)`：声明当前事务对该块的最终意图是撤销。
- `Commit(tid)`：提交当前事务；空事务提交会返回 `ErrEmptyTransaction`。
- `Checkpoint(upto)`：声明事务号不大于 `upto` 的事务已经写回磁盘；可插入未提交事务的记录之间，`upto` 必须单调不递减且不超过已提交事务数。

事务号从 1 开始连续分配。参数错误统一返回可由 `errors.Is(err, ontology.ErrInvalidArgument)` 识别的哨兵错误；当前事务达到容量时为 `ErrTransactionFull`。所有被拒绝的操作都不会改变日志。

### 前缀恢复

`Recover(disk, n)` 只读取日志前 `n` 条记录，用于模拟第 `n` 条记录刚落盘后断电。它不修改输入 `disk`，也不继续修改日志；返回新的块映像和分类计数。

恢复规则如下：

1. 当前前缀内没有对应 `Commit` 的事务完全不生效；其中的 `Write` 和 `Revoke` 会计入 `Ignored`。
2. 前缀内最后一条 `Checkpoint` 的 `upto` 记为 `K`，没有检查点时为 0。
3. 对每个已提交事务，只查看同事务内每个块的最后一条 `Write` 或 `Revoke`；最后一条为 `Revoke` 时该事务对该块撤销生效。每块取最大的撤销事务号组成撤销表。
4. 按日志顺序处理已提交事务中的每条 `Write`，判定次序固定为：
   - `tid <= K`：计入 `Checkpointed`，不再检查撤销或磁盘版本。
   - 撤销表中该块的事务号 `>= tid`：计入 `Skipped`。
   - 初始 `disk` 中该块版本 `>= tid`：计入 `Stale`；判断始终使用传入磁盘，不使用回放中生成的新版本。
   - 否则写入结果映像，版本记为 `tid`，计入 `Applied`。

同一事务内对同一块的多条 `Write` 都单独判定并分别计数；后一次应用会覆盖前一次映像内容。空载荷写入会在结果中生成版本大于 0 的空载荷块，与块从未出现在映像中不同。任意前缀下，`Applied + Skipped + Stale + Checkpointed` 等于该前缀内已提交事务的 `Write` 总数。

### 本地验证

```bash
# 普通测试（含 2000 组随机日志与朴素回放对照）
go test ./ontology

# 查看随机日志输入、前缀、输出和每条 Write 的判定依据
go test ./ontology -run TestRandomLogsMatchNaiveRecovery -v

# 竞态检测
go test -race ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
