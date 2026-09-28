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

## 两阶段本地到全局预聚合（`aggregation` 包）

### 流程与合并规则

- **本地阶段** `AggregateBatch([]RowOp) (map[string]PartialAgg, error)`：把一批
  到达的行按组折叠为部分聚合——每组产出 `Sum`、`Count` 与 `Values`（值 → 净增减行数）。
  全零（和、计数均为 0 且无残留值）的组会被过滤，**不得发送**到全局阶段。
- **全局阶段** `(*Engine).Submit(map[string]PartialAgg) error`：只接收部分聚合。
  求和与计数直接相加；合并后计数归零的组立即从结果中删除，后续批次可重建同名组。
- **结果查询** `(*Engine).Snapshot() map[string]Metrics`：返回每组的和、计数、
  平均值与去重计数四个指标的一致快照。

### 平均值与去重计数的拆分要求

- **平均值可拆分但不可直接合并**：必须以 `Sum` 与 `Count` 两项分别下推、分别合并，
  仅在查询时相除（`Avg = Sum / Count`）。直接对各批平均值求平均会得到错误结果。
- **去重计数不可拆分**：不能按批各自去重再相加，必须把 `值 → 净行数` 的映射随部分
  聚合下推到全局状态，由全局按值维护行数，去重计数为当前存活值的个数。

### 边界与错误类别

非法输入整体拒绝（原子拒绝），被拒批次不改变全局状态与已接受批次数，失败不留痕。
错误以 `*aggregation.Error` 返回，`Code` 互不相同、可区分：

| Code | 常量 | 含义 |
|---|---|---|
| 1 | `ErrCodeInvalidOp` | 非法操作类型（非 Add/Retract） |
| 2 | `ErrCodeEmptyGroup` | 空组名（本地与全局阶段均校验） |
| 3 | `ErrCodeRetractMissingRow` | 撤回不存在的行（组或值行数将变负） |
| 4 | `ErrCodeTooManyGroups` | 合并后存活组数超过 `NewEngine(maxGroups)` 上限 |

### 并发与可复现性

`Submit` 与 `Snapshot`/`AcceptedBatches` 由互斥锁保护，可被多个执行体并发调用。
合并满足交换律与结合律，同一串行按任意合法方式切批，最终快照完全相同。

### 本地验证方法

```bash
go test ./aggregation          # 单元测试：四指标合并、全零过滤、删除重建、原子拒绝
go test -race -v ./aggregation # 竞态检测；日志打印每批输入、部分聚合与判定依据
```

测试以 `recompute`（对完整串行从头批量重算）为参照，校验任意切批结果一致。
