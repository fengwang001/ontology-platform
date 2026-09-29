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

## 事务消息分区日志（`txnlog`）

`txnlog` 实现单分区事务日志：数据记录与提交/中止控制标记交错追加，
通过高水位（high watermark）与稳定位点（LSO）支撑已提交读（read-committed）。

### 事务归属

- 每条记录追加时获得从 0 开始的连续位点；位点即日志下标。
- 某生产者的一个事务 = 该生产者自上一个标记之后写入的全部数据记录；
  第一条数据的位点即**事务首位点**（无进行中事务时，写数据隐式开启事务）。
- 下一条提交/中止标记结束该事务；同一生产者同时至多一个进行中的事务。
- 不同生产者的事务可以任意交错。

### 高水位与稳定位点

- **高水位**：位点 `< hw` 的记录对消费者可见，只进不退，且不得超过日志末端。
- **稳定位点** = `min(hw, 所有首位点 < hw 且尚无可见结论的事务首位点)`。
  “结论可见”指结束标记已写入且标记位点 `< hw`。
  稳定位点只进不退，是已提交读的可见性上界。

### 已提交读（`ReadCommitted(from, limit)`）

- 只返回位点位于 `[from, 稳定位点)` 内、所属事务以**提交**结束的数据记录。
- 绝不返回控制标记，绝不暴露未决或已中止事务的数据。
- 同一稳定位点下重复读取结果完全一致（可复现）。

### 边界与错误类别

所有非法输入整体拒绝，日志、事务表、高水位、稳定位点均不变（失败不留痕），
错误值互不相同、可用 `errors.Is` 区分：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidProducer` | 生产者标识为空 |
| `ErrNoOngoingTransaction` | 无进行中事务却写提交/中止标记 |
| `ErrInvalidHighWatermark` | 高水位回退或越过日志末端 |
| `ErrInvalidReadStart` | 读取起点为负或超过稳定位点 |
| `ErrInvalidReadLimit` | 读取条数上限非正数 |

边界约定：空日志稳定位点为 0；从稳定位点本身读取返回空（合法）；
高水位可以原地不动（等于当前值合法）。

### 并发

`AppendData`、`AppendMarker`、`AdvanceHighWatermark`、`ReadCommitted` 等
全部导出方法均可被多个执行体并发调用（内部由读写锁保护），
稳定位点单调性、已提交读不暴露未决/中止数据等不变量在并发下依然成立。

### 本地验证

```bash
# 单元测试（含竞态检测、逐步日志：每步输入、稳定位点与判定依据）
go test -race -v ./txnlog/

# 覆盖场景：同一生产者先提交后中止、稳定位点推进、非法输入拒绝后状态不变、
# 并发交错、批量脚本与独立参照模型逐步比对、读取可复现、控制标记不外泄
go test -race ./txnlog/ -run 'TestStableOffsetAdvance|TestBatchReferenceConsistency' -v
```
