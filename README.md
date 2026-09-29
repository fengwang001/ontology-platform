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

## 近似分位数维护器

`quantile.Maintainer` 位于 `quantile` 包，用不超过预算数量的质心表示已加入的全部有限 `float64` 值，并额外保留真实集合以支持撤回后的确定性重算和误差核对。

### 质心与合并

- 每个 `Centroid` 记录 `Mean`、`Count`、`Min`、`Max`；质心列表始终按 `Mean` 升序。
- 新增值先插入一个单点质心，再排序；质心数超过预算时反复选择“相邻计数和最小”的一对。
- 并列的最小相邻对选择最左侧对，以保证相同输入序列产生可复现结果。
- 合并两个质心时，新计数为两计数之和，新均值为按计数加权的平均值，同时更新最小/最大值。
- `Merge` 会追加另一个实例的质心副本、按均值排序并压缩到接收方预算；接收方计数等于两者计数之和，被合并实例保持不变。

### 查询与插值

每个质心的代表秩位于其连续秩区间的中点：若前面已有 `b` 个值、当前质心计数为 `c`，其零基代表秩为 `b + (c-1)/2`。查询使用目标秩 `q*(n-1)`：

- 目标秩落在质心代表秩上时返回该质心均值。
- 目标秩落在两个相邻代表秩之间时，在两个均值之间按目标秩距离做线性插值。
- 未压缩数据因此与常用的 `(n-1)` 基线性分位点完全一致；压缩后的结果仍由固定质心和固定运算顺序确定。

### 撤回与误差上界

- `Withdraw` 先完成值合法性和存在性校验；不存在或非有限值会整体拒绝，不改动任何状态。
- 撤回成功后删除一个真实值，对剩余真实集合升序排序，按相同预算重新生成并压缩质心。
- `ErrorBound(q)` 返回保守上界：估计值到已观测最小值或最大值的最大距离。
- `ExactQuantile(q)` 用保留的真实集合计算精确线性分位点；本地可断言 `abs(Quantile(q)-ExactQuantile(q)) <= ErrorBound(q)`。
- `SelfCheck` 校验预算、质心升序、计数正数、均值范围、总计数和真实集合范围。

并发方面，`Quantile`、`ExactQuantile`、`ErrorBound`、`Count`、`Snapshot` 和 `SelfCheck` 使用读锁，可并发调用；状态变更使用写锁串行化。测试中以 `math.Float64bits` 比较并发查询结果，确保逐位相同。

### 错误原因

公开哨兵错误可区分失败原因：`ErrInvalidBudget`、`ErrInvalidValue`、`ErrInvalidQuantile`、`ErrEmpty`、`ErrValueNotFound`、`ErrNilMaintainer`、`ErrMergeSelf`。任何失败路径都在修改质心、真实集合或计数之前返回。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
