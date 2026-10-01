# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 异常表栈展开器（`unwind` 包）

`unwind` 实现按异常表驱动的栈展开：函数登记异常表，抛出异常时沿调用帧逐层查表，
命中后截断帧栈并把命中帧恢复到处理点。

### 两种 pc 的含义

- **栈顶帧**的 pc 是正在执行的指令位置，查表时直接使用 `p = pc`。
- **其余各帧**的 pc 是返回地址（调用指令的下一条位置），查表时使用 `p = pc - 1`，
  即回到调用指令本身。因此返回地址恰等于区间终点时调用指令仍在区间内（命中），
  恰等于区间起点时调用指令在区间外（不命中）。

### 异常表与查表规则

- 条目为 `(起点, 终点, 处理点, 类型)`，区间为半开 `[起点, 终点)`；类型 `0` 匹配任意类型。
- 抛出类型 `t`（正整数）时从栈顶向下逐帧查表，每帧按其函数表中的**声明序**取第一个
  满足 `起点 ≤ p < 终点` 且 `类型 == 0 或 类型 == t` 的条目。
- 命中后弹出该帧之上的所有帧，该帧 pc 设为处理点，返回命中帧的栈下标与处理点；
  全部帧都不命中则报 `ErrUncaught`，帧栈保持原样。

### 错误与检查优先级

所有被拒绝的操作整体失败且不改变函数表与帧栈，原因可用 `errors.Is` 区分：

- **登记**（先查名字，再按声明序逐条检查条目，同一条目内按下列顺序，遇第一处即报）：
  `ErrFunctionExists`（函数名已存在）→ `ErrInvalidRange`（起点 ≥ 终点）→
  `ErrHandlerInRange`（处理点落在自身区间）→ `ErrNegativeType`（类型为负）。
- **压帧**：`ErrFunctionNotFound`（函数不存在）→ `ErrNegativePC`（pc 为负）→
  `ErrZeroReturnPC`（非栈底帧 pc 为 0，返回地址不可能为 0）。
- **抛出**：`ErrNonPositiveType`（类型不为正整数）→ `ErrEmptyStack`（栈为空）→
  `ErrUncaught`（未捕获，栈不变）。

### 并发与确定性

登记、压帧、抛出与查询（`Stack` / `Entries`）都可并发调用，内部以互斥锁串行化，
结果等价于某个串行顺序；同名函数并发登记恰有一个成功，其余得到 `ErrFunctionExists`。
相同的操作序列重放得到完全相同的展开结果与帧栈。

### 本地验证

```bash
# 全部测试（含朴素逐步模拟对照与边界用例，日志打印输入/输出/判定依据）
go test -v ./unwind/

# 竞态检测
go test -race ./unwind/
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
