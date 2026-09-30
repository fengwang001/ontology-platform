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

## merger：分片扇出查询的部分结果合并器

`merger` 包将同一聚合查询发往多个分片，在并发上限与截止时间内收集结果，
并在部分分片失败时给出带可信范围的答案：既不夸大（区间覆盖真值），
也不漏报不确定性（缺失分片的上界全部计入）。

### 可信范围推导

每个分片预先登记行数上界 `RowBound` 与取值上界 `ValueBound`（非负整数）。
全部成功时五种聚合都给出精确值；有分片缺失时：

- **计数 Count**：`[已收到计数之和, 已收到计数之和 + Σ缺失行数上界]`。
  下界成立因为缺失分片计数 ≥ 0；上界成立因为缺失分片计数 ≤ 其行数上界。
- **求和 Sum**：`[已收到和, 已收到和 + Σ缺失行数上界×缺失取值上界]`。
  单个缺失分片的和不超过其行数上界与取值上界之积。
- **最小值 Min**：只给「真值 ≤ 已收到最小值」——缺失分片可能含有更小的值，
  下界除 0（非负整数）外无法确定。
- **最大值 Max**：`[已收到最大值, max(已收到最大值, 缺失取值上界最大值)]`。
  缺失分片的值不超过其取值上界，因此上端为两者较大者。
- **前 K 名 TopK**：合并已收到数据取前 K；其中**严格大于**全部缺失分片
  取值上界的前缀标为确定（`CertainPrefix`）——缺失分片不可能贡献
  ≥ 该值的元素把它挤出前 K。等于上界的值不确定，因为缺失分片可能
  并列或反超。

无分片成功时置 `Inconclusive`，不给出任何结论。区间上界的算术采用
饱和运算（`satAdd`/`satMul`），溢出时钳到 `math.MaxUint64`，
保证上界永不回绕而夸大或漏报。

### 故障分类

四类情形分别计数（`Answer.Errors/Timeouts/Violations/Duplicates`），
且都按缺失处理：

- **返回错误**：分片查询返回非超时错误。
- **超时**：截止时仍未响应，或返回 `context.DeadlineExceeded`。
- **违反登记上界**：计数超行数上界、求和超行数×取值上界、最值超取值上界、
  TopK 个数超 `min(K, 行数上界)` 或元素超取值上界；未登记分片的返回、
  以及同一分片内容冲突的多次返回也归入此类（无法核对，不可信）。
- **重复返回**：同一分片内容一致的多次返回只取首个，后到者丢弃并计数。

重复与冲突的判定按分片分组后进行，与到达顺序无关：同一组响应以任意
顺序合并，得到的 `Answer` 逐字段相同（由 `TestMergeOrderIndependence`
全排列验证）。

### 发出前整体校验

以下情形在发出任何请求前整体拒绝，且原因可用 `errors.Is` 区分：
分片列表为空（`ErrNoShards`）、分片名重复（`ErrDuplicateShard`）、
K 非正（`ErrInvalidK`）、并发上限非正（`ErrInvalidConcurrency`）、
截止时长非正（`ErrInvalidDeadline`）、聚合未知（`ErrUnknownAggregation`）。

### 并发与截止

`Executor` 以信号量限制在途请求数不超过 `Concurrency`，并用原子计数
报告峰值（`Answer.PeakConcurrency`）。截止（`Deadline`）到达时未响应的
分片记为超时；截止后迟到的结果直接丢弃，不改变已给出的答案。

### 本地验证

```bash
# 全部测试（日志含输入、输出与判定依据，需 -v 查看）
go test -v ./merger/

# 竞态检测
go test -race ./merger/

# 单个用例，例如 TopK 确定前缀边界
go test -v -run TestMergeTopKCertainPrefixBoundary ./merger/
```
