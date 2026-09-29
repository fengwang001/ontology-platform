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

## 指标注册与聚合器（`metrics` 包）

`metrics.Registry` 维护计数器（`KindCounter`）与直方图（`KindHistogram`）两类指标，序列（series）由“指标名 + 标签集合”唯一标识，标签集合的给出顺序不影响归一化结果。

### 序列名额与溢出

- 注册时声明类型、允许的标签名集合（必须恰好全部给出，不能多也不能少）与直方图桶上界。
- 同名同定义重复注册是幂等的；同名不同定义整体拒绝。
- 每个指标最多保留 `N`（`NewRegistry(maxSeries, …)`）条**普通序列**；已有序列照常累加。
- 名额满后，新标签集合折叠进该指标**唯一的溢出序列**（`SeriesSnapshot.Overflow == true`）；溢出序列不占名额、永不回收，后续所有新标签集合都并入它。

### 回收并入语义

- 时钟由 `NewRegistry` 注入；调用 `ReclaimIdle()` 时，闲置时长超过 `idleTTL` 的普通序列被回收。
- 回收时其计数器累计值（及直方图各桶计数）整体并入溢出序列，并释放名额；指标总量不变。
- 同一标签集合再次出现时视为一条从零开始的新序列；若名额仍被占用，则再次进入溢出序列。

### 桶边界与累计导出

- 桶上界必须为有限数且**严格递增**，否则注册整体拒绝；末尾隐含 `+Inf` 桶。
- 观测值**恰好等于**某上界时落入该上界对应的桶（`<= bound` 语义）。
- `Export()` 中直方图的 `Buckets`/`BucketTotals` 均为**累计计数**，末桶累计值等于该序列/该指标的观测总数。
- 每次导出在单把锁内拷贝状态，是每个指标的一致快照：任意时刻 `sum(各序列 Count) == 已接受上报总和`，直方图 `末桶累计 == 观测总数`。回收只做“普通序列 → 溢出序列”的搬迁，因此守恒恒成立。

### 拒绝原因计数

被拒绝的注册或上报不改变任何状态，并按原因分别计数（`RejectionCounts()`）：指标未注册、同名不同定义、标签缺失、标签不在允许集合内、计数器增量为负（含 NaN）、观测值为 NaN、桶上界非严格递增、类型不匹配等。

### 本地验证

```bash
# 全量测试（含竞态检测，测试日志经 t.Logf 打印输入/输出/判定依据）
go test -race -v ./...

# 仅 metrics 包
go test -race -v ./metrics

# 覆盖率与静态检查
go test -cover ./...
go vet ./...
gofmt -l .
```
