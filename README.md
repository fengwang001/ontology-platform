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

## 数据批次质量门禁（`qualitygate` 包）

对乱序到达的数据批次进行质量裁决：放行、告警放行或阻断。所有方法可并发调用，
效果等价于某个串行顺序（内部用一把互斥锁保护全部状态）。

### 批次与配置

- 批次 `Batch{Seq, Rows, Nulls}`：正整数序号 `Seq`、行数 `Rows`、空值行数 `Nulls`。
- `Config`：
  - `MinRows`：规则一行数下限。
  - `MaxNullNum / MaxNullDen`：规则二空值率上限（分数表示，保证精确比较）。
  - `BaselineK`：基线最多取 K 个批次。
  - `LoPercent / HiPercent`：规则三相对基线的下/上偏差百分比（整数）。
  - `MaxBlocks`：M，连续阻断达到该值时通道暂停。

### 裁决规则（按顺序评估，结论列出全部未满足规则）

1. 阻断：`Rows < MinRows`。
2. 阻断：`Rows > 0` 时评估空值率，交叉相乘 `Nulls*MaxNullDen > Rows*MaxNullNum`
   即超限；`Rows == 0` 时不评估本规则（由规则一阻断）。
3. 告警：存在基线时，`Rows*100 < 基线*(100-LoPercent)` 或
   `Rows*100 > 基线*(100+HiPercent)`；恰等于边界不算未满足。

任一阻断规则未满足即阻断并入隔离区；否则放行，仅规则三未满足时结论为
`warning`（告警放行）。

### 基线取法

- 候选集：序号严格小于本批次、且已放行进入基线集合的批次，即普通放行、
  告警放行、人工放行；隔离与丢弃批次不参与。
- 在候选中取序号最大的至多 `BaselineK` 个（按序号而非到达次序，因此乱序
  到达不影响结果）。
- 将这些批次的行数升序排列，取 `rows[(c-1)/2]`（c 为个数，整除）；偶数个
  时即下中位数。没有候选批次时不评估规则三。
- 已裁决批次不因之后到达的更小序号批次而重算（但隔离批次重投时，基线仍按
  其当前序号重新计算，可能纳入后来已放行的更小序号批次）。

### 隔离区操作

- `Resubmit`：同序号、新的行数/空值数重新裁决；连续阻断计数含重投裁决。
- `ManuallyRelease`：人工放行，进入基线集合；不计入也不清零连续阻断数。
- `Discard`：丢弃，此后该序号不接受任何操作；同样不影响连续阻断数。

### 连续阻断与暂停

- 连续阻断数按裁决顺序统计（提交与重投的阻断都计）；任何放行（含告警放行）
  清零；人工放行与丢弃既不计入也不清零。
- 达到 `MaxBlocks` 时通道暂停：暂停期间提交与重投一律拒绝且不裁决；
  人工放行与丢弃不受影响。
- `Resume` 恢复通道并清零连续阻断数。

### 拒绝原因（整体拒绝，不改变任何状态）

- 提交、重投先判：通道暂停（`ErrPaused`）→ 批次非法（`ErrInvalidBatch`：
  序号非正、行数为负、空值数为负或大于行数）。
- 提交再判：序号已存在（`ErrSeqExists`）。
- 重投、人工放行、丢弃再判：序号不存在（`ErrSeqNotFound`）→ 不在隔离区
  （`ErrNotQuarantined`，并可进一步用 `errors.Is` 区分
  `ErrAlreadyPassed` 与 `ErrAlreadyDiscarded`）。

### 审计日志

传入 `New` 的 `Logger` 会对每次调用打印输入、输出与判定依据（基线中位数、
参与基线的序号、未满足规则列表、连续阻断数、暂停状态、拒绝原因）；传 `nil`
时使用标准库 logger。

### 本地验证

```bash
# 若 go 不在 PATH，可先：export PATH=$PATH:/usr/local/go/bin
go test ./...
go test -race -v ./qualitygate
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out
gofmt -l . && go vet ./...
```

`qualitygate` 包测试覆盖：乱序到达按序号取基线、K 个上限、偶数下中位数、
各规则边界恰等、行数 0 跳过规则二、隔离批次人工放行后进入后续基线、
连续阻断触发暂停与恢复、全部拒绝原因与状态不变、以及并发可串行化（`-race`）。
