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

## 主键变更拆分与分区投递（`pkchange` 包）

位于 `pkchange/`，把源表变更批拆成下游可识别的事件，按主键哈希分区顺序投递，
保证下游视图与源表始终一致。入口是 `pkchange.Store.Apply`，纯函数
`pkchange.Split` / `pkchange.Merge` / `pkchange.PartitionByKey` 可独立使用。

### 拆分规则

一批变更按顺序逐条校验并拆分，事件只有两种：`write(key,row)` 与 `delete(key)`。

- 插入 → 一次 `write`；删除 → 一次 `delete`。
- 主键不变的更新 → 仅一次 `write`（不产生删除）。
- 主键变化的更新 → 先 `delete(旧键)`，再 `write(新键, 新数据)`。
- 校验基于「源表快照 + 批内此前已通过的变更」的投影状态，因此支持链式主键变更
  （如同批内 `a → a1 → a2`），也能识别「批内删后重插」。

### 合并规则

拆分得到的事件序列按主键合并：**同一个键只保留它在拆分序列中的最后一条事件**。
合并后按各键「最后一次出现」的先后排序输出，顺序完全确定。

### 分区规则

- 对合并后事件的主键做 FNV-1a 哈希后对分区数取模（`KeyPartition`），同一键永远落在同一分区。
- 每个分区内严格保持全局合并序列中的顺序，按分区下标组织结果返回，供下游各分区按序消费。

### 拒绝原因（整批原子拒绝，不改变任何状态）

| `RejectReason` | 判定依据 |
| --- | --- |
| `invalid_key` | 主键去空白后为空，或包含 NUL 字节（插入/删除/更新的新旧键均校验） |
| `key_exists` | 插入键已存在，或主键变更的目标键已存在（含本批内刚写入的键） |
| `key_not_found` | 更新/删除的键在快照或批投影中不存在（含本批内已删除的键） |
| `batch_too_large` | 批内行数超过 `batchLimit`（默认 `DefaultBatchLimit = 1000`） |
| `invalid_change` | 未知的变更操作类型 |

拒绝发生在任何提交之前：源表、下游视图、已产出事件序列三者都不会改变；
拒绝后可继续提交后续合法批。

### 一致性与确定性

- `Store` 用读写锁串行化提交，`Snapshots()` 在同一次读锁内返回源表与视图，
  并发读永远看到两者相等的同一已提交版本。
- 所有对外快照与事件均为深拷贝，调用方修改不影响内部状态。
- 无随机、无 map 遍历依赖：同一输入序列重复计算，产出的事件与分区结果逐字节相同。
- 日志（`log/slog`）打印每批的输入、逐条判定依据（`decision`）、拆分事件、
  合并结果、各分区输出以及拒绝原因。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -v ./...

# 只跑 pkchange 包
go test -race -v ./pkchange

# 覆盖率
go test -coverprofile=coverage.out ./pkchange
go tool cover -html=coverage.out

# 可运行示例（打印输入、拆分、合并、分区与拒绝日志）
go run ./cmd/pkchange-demo

# 静态检查
gofmt -l .
go vet ./...
```

主要测试：

- `TestSplitChainedKeyChanges` / `TestChainedKeyChangesThroughStore`：主键链式变更。
- `TestUpdateSameKeyProducesNoDelete`：主键不变只产生一次写入。
- `TestSplitRejections`：主键非法、键已存在、键不存在、超限、未知操作等可区分拒绝。
- `TestApplyRejectionChangesNothing`：被拒绝批不改变源表、视图与已产出事件。
- `TestConcurrentReadsNeverSeeInconsistentState`：并发读始终看到源表与视图相等。
- `TestDeterministicReplay`：同序列反复计算结果完全相同。
- `TestLogsContainInputSplitAndPartitions`：日志包含输入、拆分、分区输出与判定依据。
