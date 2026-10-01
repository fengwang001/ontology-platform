# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 对手方净敞口限额控制器（`limit` 包）

`limit` 包实现对手方净敞口限额控制器，按对手方汇总各品种净头寸，
对同组品种的多空对冲部分计入基差缓冲，并在成交与抵押品退还前检查
敞口是否越限。

### 盯市值、对冲缓冲与敞口公式

每个对手方在每个品种上持有带符号净头寸 `q`（买入为正）与已存抵押品
`G`。对品种组 `g`：

- 多头值 `Long_g = Σ max(q, 0) × 标记价格`（组内求和）
- 空头值 `Short_g = Σ max(-q, 0) × 标记价格`（组内求和）
- 对冲部分 `H_g = min(Long_g, Short_g)`

盯市值 `W = Σ q × 标记价格`（全部品种，带符号）。计入基差缓冲后的
估值：

```
W' = W + Σ_g ceil(Λ × H_g / 10000)
```

其中 `Λ` 为创建控制器时给定的基差缓冲率（万分比，0 到 10000）。
缓冲**逐组向上取整后再求和**（不是先求和再取整）；`Λ × H_g` 可能
超出 64 位，实现中使用大整数（`math/big`）运算。敞口：

```
E = max(0, W' − G)
```

### 限额判定

限额判定只作用于 `Trade` 与 `Release`：先按操作后的状态算出 `E′`，
当且仅当 `E′ ≤ L`（敞口上限）**或** `E′ ≤ E`（操作前敞口）时放行。
也就是说，**不增加敞口的操作总是放行**，即使对手方当前已经超限；
被拒绝的操作不改变任何状态。`SetPrice`、`SetGroup`、`Post` 不做限额
检查，因此一个对手方的敞口只可能在 `SetPrice` 或 `SetGroup` 时由不
超限变为超限。

### 超限标记与名单次序

每次被接受的状态变更操作之后，控制器按对手方字节序升序重新核对
全部对手方（该核对是操作的一部分，对外原子）：

- `E > L` 且尚未标记的，标记并取下一个全局递增序号；
- `E ≤ L` 且已标记的，取消标记（再次超限时取新序号）；
- 仍超限的保持原序号不变。

`Breaches()` 返回当前已标记的对手方及其当前敞口，按标记序号升序
（先进入超限的在前，与登记先后无关）；名单中的序号两两不同。相同
的操作序列重放得到完全相同的放行/拒绝序列与超限名单。

### 并发

所有操作与查询可并发调用，内部以互斥锁串行化，结果等价于某个串行
顺序。

### 本地验证

```bash
# 全部测试（含例一/例二完整序列、边界用例、2000 组随机序列与
# 朴素大整数模拟的对照）
go test ./limit/

# 竞态检测
go test -race ./limit/

# 查看随机对照测试的输入、输出与判定依据日志
go test -v -run TestRandomAgainstModel ./limit/
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
