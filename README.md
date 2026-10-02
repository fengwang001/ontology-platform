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

## 检查点刷脏节奏调度器（`checkpoint` 包）

`checkpoint.Scheduler` 依据检查点开始后的已用时间与已产生日志量，计算累计应已写出的脏页配额，用于控制刷脏节奏。

### 两条进度与取最大规则

`Begin(now, walPos, dirty)` 以 `(s, w0) = (now, walPos)` 为起点、以 `N = dirty` 为脏页总数开始一次检查点。之后每次 `Tick(now, walPos)` 分别计算两条进度配额并取较大者：

- 时间配额：`q1 = min(N, ⌊N·e·1000 / (T·F)⌋)`，其中 `e = now − s`
- 日志配额：`q2 = min(N, ⌊N·u·1000 / (W·F)⌋)`，其中 `u = walPos − w0`
- 累计配额：`Q = max(q1, q2)`，`need = max(0, Q − 已写出数)`

即时间与日志两条进度各自独立推进，任何一条超前都以前进更多者为准。

### 取整与封顶

全部按精确整数运算向下取整（`math/big` 任意精度），`N`、`e`、`u` 达 2^40 量级时乘积不溢出；`q1`、`q2` 各自封顶于 `N`，因此 `Q ≤ N` 恒成立。当 `e` 恰等于 `T·F/1000`（或 `u` 恰等于 `W·F/1000`）时对应配额恰为 `N`。

### 完成条件

`Wrote(k)` 登记写出 `k` 页；已写出数达到 `N` 时检查点完成，调度器回到空闲且已完成数加一。`Begin` 时 `dirty` 为 0 表示该检查点立即完成，不进入进行态。检查点之间没有最小间隔限制。`Status` 返回是否在进行、`N`、已写出数与已完成数（空闲时 `N` 与已写出数均为 0）。

### 误用拒绝

构造参数（`T` 非正、`W` 非正、`F` 不在 1..999）与 `Begin`/`Tick`/`Wrote` 的各类误用（负值、状态冲突、时刻或日志位置倒退、超量写出等）分别以可区分的哨兵错误整体拒绝，每个操作内按规定顺序只报第一个；被拒绝的操作不改变任何状态。所有方法可并发调用，效果等价于某个串行顺序。

### 本地验证

```bash
# 单元测试（边界取整、封顶、溢出、误用拒绝、并发）
go test ./checkpoint/

# 含 big.Rat 朴素实现对拍（2000 组随机序列）与竞态检测
go test -race -run 'TestAgainstNaiveModel|TestDeterministicReplay' -v ./checkpoint/
```
