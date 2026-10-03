# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 短信分段与计量器

包 `ontology`（`sms.go`、`codec.go`）提供 `Meter`：按字符集判定编码、把文本
贪心切成短信分段，并按账户周期累计段数、上期结转的首档容量与国际倍率精确计费。

### 构造与参数范围

`NewMeter(P, T0, p1, p2, MI)`，单位金额为“厘”：

- `P`：周期长度，`[1, 10^9]`。
- `T0`：基础首档容量（段数），`[0, 10^6]`。
- `p1`、`p2`：首档 / 次档单价，均 `[0, 10^6]`。
- `MI`：国际倍率百分比整数，`[100, 1000]`。

越界返回 `invalid_argument`。

### 字符集与单位数

文本按 Unicode 码点处理，必须是合法 UTF-8 且非空。

- 基本集 `B`：英文字母、数字、空格、换行，以及
  `.,!?:;-_@#%&*()'"+=/<>$`。
- 扩展集 `X`：`{` `}` `[` `]` `~` `^` `|` `\` 与 `€`。
- 全部码点都属于 `B ∪ X` 时编码为 `GSM`，否则为 `UCS2`。

单位数：

- `GSM`：`B` 字符 1 单位，`X` 字符 2 单位。
- `UCS2`：码点不大于 `U+FFFF` 占 1 单位，大于 `U+FFFF`（如 emoji）占
  2 单位；此时即使是 `X` 字符也只占 1 单位。

### 装段规则

- 总单位数不大于 `160`（GSM）或 `70`（UCS2）时为 1 段。
- 超过后按每段 `153`（GSM）或 `67`（UCS2）单位，自左向右贪心装段。
- 占 2 单位的字符不得拆开；若段内只剩 1 单位、放不下该 2 单位字符，则
  该字符整体移入下一段，上一段尾部留空 1 单位。
- 段数大于 `10` 报 `too_many_segments`。

例：152 个 `a`、一个 `{`、10 个 `b` 共 164 GSM 单位，分为 `[0,152)` 与
`[152,163)`；66 个汉字、一个 `U+1F600`、5 个汉字共 73 UCS2 单位，同理分为
`[0,66)` 与 `[66,72)`。

### 账户与周期结转

- `Deposit(a, x)`：`x ∈ [1, 10^12]`，存入后余额不超过 `10^15`。账户不存在
  时先创建；账户编号为空、金额越界或超限均报 `invalid_argument`，余额不变。
- 账户记录周期号 `k = floor(now / P)`、本期已用段数 `u`、本期首档容量 `T`。
  首次发送视为周期 `k'`、`u=0`、`T=T0`。
- 跨周期：`k' == k` 时 `u,T` 不变；`k' > k` 时，上期段数取“`k' == k+1` 时
  的 `u`，否则（隔了至少一个空周期）为 `0`”，新周期 `u=0`、
  `T = T0 + floor(上期段数 / 4)`。

### 计价

本条第 `j` 段的全局序号为 `u+j`：序号不大于 `T` 按 `p1`，其余按 `p2`，
各段价格之和为 `s`。

- 国内费用：`s`。
- 国际费用：`ceil(s × MI / 100)`，只在整条短信上取整一次。

`Send(a, text, international, now)` 成功后扣余额、`u += 段数`、更新 `k,T`
并推进全局最大时钟；费用大于余额报 `insufficient_balance`（恰等通过）。
`Quote(...)` 与随后紧接的 `Send` 逐字段相同，但不改任何状态。

### 拒绝原因与顺序

按以下顺序只报第一个：`invalid_argument`（构造越界、空账户、空文本或非法
UTF-8、`now ∉ [0, 10^12]`）→ `account_not_found`（仅 Send/Quote）→
`clock_skew`（`now` 小于全体账户已接受 Send 的最大 `now`，初值 0；Quote 也
检查但不推进）→ `too_many_segments` → `insufficient_balance`。被拒操作不改变
余额、`k`、`u`、`T` 与最大时钟，也不发生周期滚动。

`Split(text)` 只读，返回编码与每段 `[起, 止)` 码点下标区间，时间与空间均与
文本长度成正比。所有方法都在互斥锁下执行，结果等价于某个串行顺序。

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

# 2000 组随机文本/操作序列对拍（朴素实现逐码点生成单位序列再切段）
# -v 会打印每组的输入、输出与判定依据
go test -run TestNaiveComparison -v ./ontology
go test -run TestNaiveComparison -count=1 ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
