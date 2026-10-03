# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 短信分段与计量器

包路径：`ontology/sms`。使用 `sms.New(P, T0, p1, p2, MI)` 创建计量器：

- `P`：周期长度，范围 `1..10^9`。
- `T0`：每周期初始首档容量，范围 `0..10^6`。
- `p1`、`p2`：首档、次档单价，范围 `0..10^6`，单位为厘。
- `MI`：国际倍率百分比，范围 `100..1000`。

### 编码与单位

文本按 Unicode 码点处理，空文本和非法 UTF-8 均拒绝。

- 基本集 `B`：英文字母、数字、空格、换行，以及 `.,!?:;-_@#%&*()'"+=/<>$`。
- 扩展集 `X`：`` {}[]~^|\ `` 和 `€`。
- 所有码点都属于 `B` 或 `X` 时编码为 `GSM`，否则编码为 `UCS2`。
- `GSM`：`B` 字符占 1 单位，`X` 字符占 2 单位。
- `UCS2`：`U+0000..U+FFFF` 占 1 单位，`U+10000` 及以上占 2 单位；此时扩展集字符也只占 1 单位。

### 装段规则

- `GSM` 总单位不超过 160 为 1 段；超过后每段容量 153。
- `UCS2` 总单位不超过 70 为 1 段；超过后每段容量 67。
- 多段时自左向右贪心装段。
- 占 2 单位的字符不可拆开；若放入当前段会超限，整体移入下一段，当前段保留空位。
- `Split(text)` 返回编码和 `[起, 止)` 码点下标区间。
- 段数超过 10 拒绝；时间与空间复杂度均与文本长度成正比。

### 账户、周期和费用

- `Deposit(a, x)`：账户不存在时创建；`x` 范围 `1..10^12`，存款后余额不得超过 `10^15`。
- 周期号为 `floor(now / P)`。
- 进入新周期时 `u=0`；若新周期恰好是下一周期，`T=T0+floor(上期 u/4)`，若至少跳过一个空周期则上期段数视为 0，`T=T0`。
- 本条第 `j` 段的全局序号是 `u+j`；序号不超过 `T` 按 `p1` 计价，否则按 `p2` 计价。
- 国内费用是各段单价之和 `s`；国际费用为整条短信统一取整一次：`ceil(s*MI/100)`。
- `Quote` 与随后紧接的 `Send` 返回相同编码、段数和费用，但 `Quote` 不修改余额、周期、已用段数或最大时钟。

拒绝原因按以下顺序只返回第一个，可用 `errors.Is` 区分：

1. `ErrInvalidArgument`：构造参数、账户名、文本、`now`、存款金额或存款后余额非法。
2. `ErrAccountNotFound`：`Send` 或 `Quote` 使用未存款创建的账户。
3. `ErrClockRolledBack`：`now` 小于已接受 `Send` 的最大时钟；`Quote` 也检查但不推进时钟。
4. `ErrTooManySegments`：段数大于 10。
5. `ErrInsufficientFunds`：费用大于余额；费用恰好等于余额允许发送。

所有被拒绝的操作都不会改变状态。

### 短信功能本地验证

```bash
# 全量测试；随机对拍日志可用 -v 查看
go test ./...
go test ./sms -run TestRandomOperationsAgainstNaiveModel -v

# 并发竞态检测
go test -race ./sms

# 静态检查
go vet ./...
gofmt -l .
```

随机测试固定种子，对 2000 组随机文本与操作序列执行朴素逐码点建模对拍，并用 `t.Logf` 打印输入、输出、分段与判定依据，结果可重放。

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
