# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## ACME 风格证书订单与授权状态机（`ontology` 包）

`ontology` 包实现了一个可精确复现的证书订单 / 域名授权状态机，所有操作
在互斥锁下执行，并发调用的结果等价于某个串行顺序；重放相同的操作序列
得到完全相同的编号、状态与错误。

### 构造参数

`New(Config{Ta, Tv, To, H, F, C, Pm})`，任一越界即以
`Kind == "config_invalid"` 的 `*Error` 整体拒绝：

- `Ta`、`Tv`、`To`、`H`：秒，范围 `[1, 10^9]`。
- `F`：失败阈值，`[1, 1000]`；`C`：nonce 池容量，`[1, 10^6]`；
  `Pm`：每账户待验证授权上限，`[1, 10^4]`。
- 时间 `now` 为 `[0, 10^15]` 的整数；所有操作共用一个全局时钟，
  被接受操作见过的最大 `now` 单调不降，更小的 `now` 报时钟回退。
  只读操作（`Status`、`GetAuthz`、`GetOrder`）也拒绝回退，但不推进时钟。

### 授权与订单编号、状态

- 授权按创建顺序编号 `z1, z2, …`；订单编号 `o1, o2, …`；
  证书序号为全局计数器，从 1 起，每次成功 `Finalize` 加一。
- 授权存储状态：`pending`、`valid`、`invalid`、`deactivated`。
  **有效状态派生**：存储状态为 `pending` 或 `valid` 且 `now >= expires`
  时为 `expired`（`now == expires` 即到期），否则等于存储状态。
- 订单存储状态：`active`、`valid`。**订单状态按 `now` 派生**：
  存储为 `valid` 则恒为 `valid`（之后授权到期也不影响）；否则
  `now >= 订单 expires` 即 `invalid`；否则任一授权有效状态属于
  `invalid`、`deactivated`、`expired` 即 `invalid`；否则全部授权
  `valid` 为 `ready`；其余为 `pending`。

### 操作与拒绝次序

带 nonce 的操作：`NewOrder`、`Deactivate`、`Finalize`；`Report` 无
账户与 nonce；`Status`/`GetAuthz`/`GetOrder` 只读。拒绝只报第一个，
类别（`Error.Kind`）依次为：

1. `param_invalid`（参数非法：配置、空账户/编号、标识符、CSR 列表、
   `now` 越界等）
2. `clock_regression`（时钟回退）
3. `nonce_invalid`（nonce 不在池中）
4. `not_found`（对象不存在或属于别的账户）
5. `state_conflict`（状态不符，`Error.Status` 带当前有效状态；
   `Report` 遇到 `expired` 也归此类）
6. `csr_mismatch`（CSR 标识符集合与订单不一致，与顺序无关）
7. `rate_limited`（`Error.Ident`/`Error.Count` 带首个被限流标识符及其
   窗口内失败数）
8. `quota_exceeded`（`Error.Pending`=p、`Error.Need`=q）

`Report` 的次序为 参数非法 → 时钟回退 → 授权不存在 → 状态冲突。
任何被拒绝的操作都不改变授权、订单、失败记录、计数器、nonce 池与
时钟，也不消费 nonce。

### 授权复用规则（NewOrder）

对每个标识符，在同一账户下有效状态为 `valid` 的授权中选择
`expires` 最大者；`expires` 相同取编号最小者。没有可复用授权时新建
`pending` 授权，`expires = now + Ta`。订单 `expires = now + To`、
存储状态 `active`。复用索引按 `(账户, 标识符)` 维护，查找时顺带把
已非 `valid` 或已到期的条目解除链接（每个授权至多一次），因此键之外的
历史授权数量不影响考察计数。

### 限流与配额

- 限流：通过 nonce 等检查后，按标识符给定顺序统计该账户该标识符满足
  `t + H > now` 的失败记录数（`t + H == now` 已出窗），达到 `F` 即报
  第一个被限流的标识符；此时不创建任何授权或订单、不消费 nonce。
  窗口外记录在统计时摊还丢弃，每条至多一次。
- 配额：`p` 为该账户当前有效状态为 `pending` 的授权数，`q` 为本次需
  新建数；`p + q > Pm` 即超限。复用不占配额；`valid`/`invalid`/
  `deactivated`/`expired` 均不计入 `p`。授权到期或离开 `pending`
  在按账户维护的待验证列表中各至多被摊还处理一次。

### Nonce 池与消费语义

`Nonce()` 无参数、总成功，依次返回 `1, 2, 3, …` 并放入有界池。池满
（个数等于 `C`）时先淘汰当前最小的一个。放入与淘汰均为摊还 `O(1)`，
与池容量无关（单调淘汰游标 + 成员集合）。带 nonce 的操作仅在被接受时
把该 nonce 从池中移除；从未发出、已消费、已被淘汰的 nonce 均报无效。
并发下 `Nonce` 不发重复值，同一 nonce 并发使用至多一个被接受。

### 本地验证

```bash
# 全量测试（含 2000 组随机序列对朴素规范模拟器的差分对照）
go test ./...

# 竞态检测
go test -race ./...

# 详细查看差分/规则用例的输入输出与判定依据
go test -run 'TestDifferential|TestSpec|TestRate|TestQuota' -v ./ontology

go vet ./...
gofmt -l .
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
