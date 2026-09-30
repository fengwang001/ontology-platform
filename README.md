# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 流水线逐跳计数审计器（`auditor` 包）

按事件时间桶汇总各阶段乱序、重复上报的收发计数，并逐桶裁决。

### 模型

- 流水线共 K 个阶段（编号 `0..K-1`），阶段 `s` 的展开倍数为 `f_s`（正整数，一条输入产生 `f_s` 条输出）。
- 桶号为 `⌊事件时间 ÷ 桶宽⌋`（`Auditor.BucketOf`）。
- 阶段对某桶上报 `(序号, 收入, 发出, 丢弃)` 四个整数，收入/发出/丢弃为累计值。
- 同一 `(阶段, 桶)` 只采用序号最大的上报；序号不大于已采用者的上报被拒绝（`ErrStaleSeq`）。
- 桶在首次被上报时才存在。

### 结算与修订

- 结算触发（先到者为准）：K 个阶段都上报过，或观察水位 `≥ 桶右端 + 宽限`；结算前裁决为**待定**。
- 结算时的首次裁决修订号为 0；已结算桶每收到新上报都重新裁决，裁决的类别、位置或差额任一变化则修订号加一。
- 迟到上报修订后的裁决与按最新上报重新推演的结果一致（修订号可因到达次序不同而不同）。

### 裁决判定次序

结算桶按阶段 `0` 到 `K-1` 依次检查，遇首个违规即为裁决：

1. **不守恒**：已上报阶段自身 `收入 × f_s ≠ 发出 + 丢弃`，记该阶段。
2. **丢失 / 重复**：该阶段到下一阶段的跳，两端都已上报才比较；下一阶段收入 `<` 本阶段发出为丢失，`>` 为重复，记跳与差额。
3. **缺报**：无违规但有阶段从未上报，记编号最小的缺报阶段。
4. **平衡**：以上皆不成立。

### 拒绝规则

阶段越界、桶号为负、计数为负、序号非正、序号过旧（各按此序只报第一个）、观察水位回退，整体拒绝并返回可区分的错误（`ErrStageOutOfRange` / `ErrNegativeBucket` / `ErrNegativeCount` / `ErrNonPositiveSeq` / `ErrStaleSeq` / `ErrWatermarkRegression`）；被拒绝的操作不改变任何桶的采用值、结算状态与修订号。

### 并发与确定性

`Report`、`AdvanceWatermark`、`Verdict` 均可并发调用（内部互斥）；每个桶任一时刻只有一个裁决。上报到达次序打乱后，同一批上报的最终采用值与裁决完全相同；相同操作序列重放结果完全相同。

### 本地验证

```bash
# 全量测试（含竞态检测与输入/输出/判定依据日志）
go test -race -v ./auditor/
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
