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

`metrics.Registry` 是带序列数上限的计数器 / 直方图注册与聚合器，支持并发
上报、回收与导出。通过 `metrics.New(N, T, opts...)` 构造：`N` 为每个指标
的普通序列名额，`T` 为空闲回收时长；时钟经 `WithClock` 注入，日志经
`WithLogger` 注入（默认丢弃，日志逐条打印输入、输出与判定依据）。

### 注册规则

- `Register(name, Definition{Type, Labels, Buckets})` 声明类型（`Counter` /
  `Histogram`）、允许的标签名集合与直方图升序桶上界；`Labels` 与给出顺序无关。
- 同名且定义完全相同（类型、标签集合、桶上界均一致）的重复注册是幂等的；
  同名不同定义按 `definition_mismatch` 拒绝。
- 桶上界必须为有限数且**严格递增**，否则按 `buckets_not_strictly_increasing`
  拒绝；末尾隐含正无穷桶。

### 序列名额与溢出

- 序列由“指标名 + 标签集合（名值对）”标识。每个指标最多保留 `N` 条普通序列；
  已有序列照常累加。
- 名额占满后出现的新标签集合全部折叠进该指标**唯一的溢出序列**。溢出序列不占
  名额、永不回收；它在第一次需要折叠（满额新集合或回收并入）时惰性创建。
- 导出快照中普通序列位于 `MetricSnapshot.Series`，溢出序列位于
  `MetricSnapshot.Overflow`。

### 回收并入语义

- `ReclaimExpired()`（以及任何时刻的导出）依据注入时钟判定：
  `now - lastUpdate > T` 才回收，空闲恰为 `T` 的序列保留。
- 被回收的普通序列：计数器值、直方图各桶计数、观测条数与观测值总和**整体并入
  溢出序列**，随后释放名额；已接受总量不变。
- 同一标签集合在回收后再现时作为一条**全新序列从零开始**；旧值已永久保存在
  溢出序列中。

### 桶边界与累计导出

- 观测值 `v` 落入第一个满足 `v <= bound` 的桶，即**恰等于某上界时落入该桶**；
  超过全部上界（含 `+Inf`，拒绝 `NaN`）落入末桶。
- 内部按桶保存独立计数；`Export()` 导出的 `BucketCounts` 为**累计计数**，
  长度为 `len(Buckets)+1`，末桶（正无穷桶的累计值）等于该序列观测总数。

### 拒绝原因

所有拒绝均为**整体拒绝**，被拒绝的注册或上报不改变任何状态，并按原因分别计数
（`Export().Rejected` / `Registry.RejectedCount`）：

- `metric_not_registered`：上报未注册指标
- `definition_mismatch`：同名不同定义、上报类型与指标类型不符
- `label_missing`：缺少允许集合中的必需标签
- `label_not_allowed`：携带了允许集合之外的标签
- `negative_delta`：计数器增量为负
- `nan_observation`：直方图观测值为 `NaN`
- `buckets_not_strictly_increasing`：桶上界不严格递增或非有限

### 一致性与守恒

全部方法互斥并发安全。每次 `Export()` 对每个指标给出一致快照：普通序列与
溢出序列之和恒等于该时刻已接受上报之和（计数器为增量总和，直方图为观测总数，
各桶总计同样守恒）。并发下哪些标签集合占到名额取决于到达顺序，但每个指标的
总量与桶总计与任意串行顺序一致；同一上报序列串行重放得到完全相同的序列划分。

### 本地验证

```bash
# 单测（含满额折叠、桶边界、累计导出、回收占名额/旧集合从零开始、
# 各类非法上报、串行重放与并发守恒）
go test -race -v ./metrics

# 覆盖率
go test -coverprofile=coverage.out ./metrics
go tool cover -html=coverage.out
```
