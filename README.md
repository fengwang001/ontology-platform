# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 流水线逐跳计数审计器（`audit` 包）

`audit.Auditor` 按事件时间桶汇总 K 个阶段乱序、重复上报的收发计数，逐桶给出唯一裁决；
结算后的迟到上报会被采纳并重新裁决，结果与“按最新上报重新推演”完全一致。

### 模型

- 阶段编号 `0..K-1`，阶段 s 展开倍数 `f_s`（一条输入产生 `f_s` 条输出）。
- 桶号 `b = ⌊事件时间 ÷ 桶宽⌋`，桶右端为 `(b+1)*桶宽`。
- 上报四元组 `(序号, 收入, 发出, 丢弃)`，后三项为累计值。
- 同一（阶段，桶）只采用序号最大的上报；序号 `<=` 已采用序号即拒绝（`ErrStaleSeq`）。
- 桶在首次被有效上报时才创建；未结算前查询裁决为 `pending`。

### 拒绝原因（固定次序，每次只报第一个）

1. 阶段越界 `ErrStageOutOfRange`
2. 桶号为负 `ErrNegativeBucket`
3. 任一计数为负 `ErrNegativeCount`
4. 序号非正 `ErrNonPositiveSeq`
5. 序号过旧 `ErrStaleSeq`（需对照已采用值，在持锁后检查）
6. 观察水位回退 `ErrWatermarkRegression`（仅推进水位操作）

被拒绝的操作不改变任何桶的采用值、结算状态、修订号与水位。

### 结算规则（先到者为准）

- 齐报结算：K 个阶段都已上报过该桶，立即结算。
- 水位结算：观察水位 `>= 桶右端 + 宽限` 时结算（水位推进时按桶号升序处理）。
- 两种触发谁先满足谁生效；结算前裁决恒为 `pending`。

### 裁决判定次序

结算（或结算后重新裁决）时按阶段 `s = 0..K-1` 依次检查，遇首个违规即返回：

1. 阶段自身（仅当该阶段已上报）：`收入*f_s != 发出+丢弃` => `unbalanced`，记阶段 s。
2. 跳 s → s+1（两端都已上报才比较；最后一个阶段无跳）：
   - 下游收入 `<` 上游发出 => `lost`，记跳 s，差额 = 上游发出 - 下游收入；
   - 下游收入 `>` 上游发出 => `duplicated`，记跳 s，差额 = 下游收入 - 上游发出。
3. 全部检查无违规：有阶段从未上报 => `missing`，记编号最小的缺报阶段；否则 `balanced`。

因此缺报会被编号更小阶段的 `unbalanced` 或跳违规压过。

### 修订规则

- 结算时的首次裁决不计修订（修订号初始为 0）。
- 已结算桶每收到一条被采纳的新上报都按最新采用值重新裁决。
- 裁决的类别、位置（阶段/跳）或差额任一变化，修订号加 1；完全相同则不变。

### 并发与确定性

- 上报、推进水位、查询（`Get`/`Buckets`/`Watermark`）可并发调用，内部以单一 `sync.RWMutex` 保证每个桶任一时刻只有一个裁决。
- 同批上报打乱到达次序后，最终采用值与裁决完全相同（修订号可能因结算前后到达次序不同而不同）。
- 相同操作序列重放，裁决与修订号完全一致。

### 本地验证

```bash
# 全量测试 + 竞态检测（测试日志打印每条输入上报、输出裁决/拒绝原因与判定依据）
go test -race -v ./audit

# 覆盖率
go test -cover ./audit

# 格式化与静态检查
gofmt -l .
go vet ./...
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
