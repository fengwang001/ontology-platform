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

## 异常表栈展开器

异常表位于 `exception` 包，核心类型为 `exception.Unwinder`。异常表条目为：

- `Start`、`End`：受保护区间，按半开区间 `[Start, End)` 匹配。
- `Handler`：命中后的恢复位置。
- `Type`：异常类型；`0` 表示匹配任意异常类型。

### 两种 PC

- 栈顶帧的 `PC` 是正在执行的指令位置，查表使用 `p = PC`。
- 栈顶以下调用帧的 `PC` 是返回地址，即调用指令的下一条位置；查表使用 `p = PC - 1`，从而让恰好在区间终点返回的调用仍按调用指令位置命中。

例如返回地址为 `20`、区间为 `[10,20)` 时，调用帧使用 `p=19`，会命中；返回地址若为区间起点 `10`，则使用 `p=9`，不会命中。

### 展开规则

抛出正整数类型 `t` 时，从栈顶向下逐帧检查：

1. 栈顶使用原 `PC`，其余帧使用 `PC-1`。
2. 在当前函数的异常表中严格按登记时的声明序遍历条目。
3. 第一个满足 `Start <= p < End` 且 `Type == 0 || Type == t` 的条目命中。
4. 命中后弹出命中帧之上的全部帧，将命中帧的 `PC` 设置为 `Handler`，并返回栈下标与处理点。
5. 所有帧均不命中时返回 `exception.ErrUncaughtException`，帧栈保持原样。

### 错误优先级

所有错误都可通过 `errors.Is` 与哨兵错误比较，拒绝的操作不会修改函数表或帧栈。

- 登记：先检查重名 `ErrFunctionAlreadyExists`；再按条目声明序检查，同一条目依次为起点不小于终点 `ErrInvalidRange`、处理点落在自身区间 `ErrHandlerInsideRange`、类型为负 `ErrNegativeEntryType`。
- 压帧：依次检查函数不存在 `ErrFunctionNotFound`、`PC < 0` 的 `ErrNegativePC`、非栈底帧 `PC == 0` 的 `ErrZeroReturnPC`。
- 抛出：先检查类型非正整数 `ErrInvalidThrownType`，再检查空栈 `ErrEmptyStack`，查表失败为 `ErrUncaughtException`。

### 并发与确定性

`Register`、`Push`、`Throw`、`Frames` 和 `Functions` 使用同一个读写锁保护，因此结果等价于某个合法串行顺序；同名函数并发登记恰有一个成功。相同操作序列按相同顺序重放会得到相同结果和帧栈。查询接口返回快照，调用方修改返回值不会影响内部状态。

### 本地验证

```bash
# 常规测试（如 GOCACHE 默认目录不可写，可显式指定缓存目录）
GOCACHE=/tmp/ontology-go-cache go test -v ./...

# 竞态检测与逐步朴素模拟日志
GOCACHE=/tmp/ontology-go-cache go test -race -v ./exception

go vet ./...
gofmt -l .
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
