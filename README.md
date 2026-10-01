# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 对手方净敞口限额控制器

控制器通过 `New(lambda)` 创建，`lambda` 是万分比整数形式的基差缓冲率，范围为 `0..10000`。公开类型与方法位于包 `ontology`：

- `Register(cp, L)`：登记非空对手方与限额 `L`。
- `SetPrice(inst, px)`：设置或更新品种标记价格，不触发限额检查，系统最多保留 1000 个品种。
- `SetGroup(inst, g)`：把已设价品种划入非空组；后一次设置覆盖前一次。未分组品种自成一组，组名等于品种名。
- `Trade(cp, inst, n)`：以当前标记价格成交带符号数量，成交后单品种净头寸绝对值不得超过 1000000。
- `Post(cp, g)` / `Release(cp, g)`：存入或退还抵押品。
- `Exposure(cp)`：查询当前敞口。
- `Breaches()`：按进入超限的先后返回当前仍超限的对手方与当前敞口。

对每个组 `g`：

- `Long_g = Σ max(q, 0) × px`
- `Short_g = Σ max(-q, 0) × px`
- `H_g = min(Long_g, Short_g)`

全对手方盯市值为带符号值 `W = Σ q × px`。基差缓冲逐组向上取整后求和：

```text
W′ = W + Σ_g ceil(lambda × H_g / 10000)
E  = max(0, W′ − G)
```

逐组分别取整，不能先把各组 `H_g` 求和后再统一取整。内部使用 128 位无符号乘法除法计算 `lambda × H_g / 10000`，测试中的朴素参考模型使用 `math/big`。

`Trade` 与 `Release` 先在临时状态上计算操作后敞口 `E′`。仅当 `E′ > L` 且 `E′ > E` 时拒绝；因此 `E′ <= L` 放行，已经超限时任何不增加敞口的操作也放行。被拒绝的操作会完整回滚，不改变头寸、抵押品、价格、分组、超限标记或序号。

每个被接受且可能改变状态的操作完成后，都在同一原子临界区内按对手方字节序重新审计全部对手方：

- `E > L` 且尚未标记：分配下一个全局递增序号。
- `E <= L` 且已标记：清除标记；再次超限时分配新序号。
- 持续超限：保留原序号。

`Breaches()` 按标记序号升序返回，所以名单次序表示首次进入当前这轮超限状态的先后；同一次操作中新进入的多个对手方按字节序分配序号。所有方法由互斥锁保护，并发调用等价于某种合法串行顺序。

### 参数错误

方法返回预定义哨兵错误，可用 `errors.Is` 区分：

- `ErrInvalidArgument`
- `ErrCounterpartyAlreadyRegistered`
- `ErrCounterpartyNotFound`
- `ErrNoPrice`
- `ErrInsufficientCollateral`
- `ErrLimitExceeded`

错误按题述优先级只返回第一个原因。例如 `Trade` 依次检查参数非法、对手方未登记、品种无价和限额超限；`Release` 依次检查参数非法、对手方未登记、抵押品不足和限额超限。

### 本地验证

常规测试：

```bash
go test ./...
```

竞态、静态检查与详细随机日志：

```bash
go test -race ./...
go vet ./...
go test -run TestRandomizedAgainstNaiveBigIntModel -v -args -verbose-random
```

随机测试重放 2000 组操作序列，逐步对照控制器与使用大整数的朴素参考模型；开启 `-verbose-random` 后会记录每步输入、返回结果、放行或拒绝依据以及暴露和超限名单。

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
