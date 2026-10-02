# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分层 JIT 热度管理器（`jit` 包）

`jit` 包实现了一个带负载反馈、串行编译队列、计数衰减与去优化惩罚的分层即时编译热度管理器。所有操作与查询可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序；相同操作序列重放得到完全相同的层级、计数、作业时刻与丢弃数。

### 构造参数

`NewManager(Config)` 校验并接收：方法数 `N`（编号 `0..N-1`）、层 1 阈值 `A1/M1/B1`、层 2 阈值 `A2/M2/B2`（要求 `M1<=A1`、`M2<=A2`）、反馈除数 `F`、队列容量 `Qc`、编译时长 `D1/D2`、衰减周期 `Pd`、冷却基数 `C` 与层 2 封禁阈值 `Kd`。参数非法返回 `ErrInvalidArgument`。

### 操作语义（固定处理顺序）

`Call(m, n, now)` 依次执行：

1. **校验**：`m` 越界、`n` 不在 `[0, 10^6]`、`now` 不在 `[0, 10^15]` 报 `ErrInvalidArgument`；`now < T` 报 `ErrClockRegression`。被拒绝的操作不改变任何状态（不安装、不衰减、不推进 `T`）。
2. **安装**：队首完成时刻 `<= now` 的作业按入队序依次出队，方法层级改为目标层并清除在途标志。每次被接受的操作检查队首不超过「安装数 + 1」次（由非导出计数器 `headChecks`/`installs` 验证）。
3. **衰减**：`g = now/Pd - e`，`i` 与 `b` 各自右移 `min(g, 62)` 位（跨多个纪元只右移一次），`e` 更新为 `now/Pd`。
4. **执行**：返回当前层级 `t`，然后 `i+1`、`b+n`。
5. **晋升判定**：仅当 `t<2`、无在途作业且 `now>=cu`（冷却结束，恰等于即可）时进行。

`Deopt(m, now)` 同样先校验、安装、衰减，仅当安装后方法处于层 2 时成功：回到层 0、`i/b` 清零、`dc+1`、`cu = now + C*dc`。否则报 `ErrNotTier2` 且不改变任何状态。`State(m, now)` 返回「先安装、再衰减」视角的只读快照（层级、`i/b/dc/cu`、队列长度与 `LF`），不修改任何状态。

### 晋升判定与负载缩放

设队列内作业数（含正在编译者）为 `q`，缩放 `s = 1 + ⌊q/F⌋`，去优化惩罚 `d = 1 + dc`：

- **H2**：`i >= A2·d·s`，或 `i >= M2·d·s` 且 `i+b >= B2·d·s`。
- **H1**：`i >= A1·s`，或 `i >= M1·s` 且 `i+b >= B1·s`。

阈值乘积可超过 int64，比较使用 `math/big` 任意精度精确进行。`dc >= Kd` 的方法被封禁层 2（H2 恒不成立，H1 与层 1 晋升不受影响）。`t=0` 时 H2 成立则跳级到层 2，否则 H1 成立升到层 1；`t=1` 时仅在 H2 成立时升层 2。

### 串行编译时刻

编译线程只有一个，作业严格串行、先进先出。有晋升目标时，若 `q >= Qc` 则本次不入队（丢弃数加一，可由 `Dropped()` 读取）；否则入队：开始时刻 `start = max(now, LF)`，完成时刻 `finish = start + D<目标层>`，`LF` 更新为该完成时刻，方法置在途标志。因此队列内完成时刻单调不降，每个方法至多一个在途作业，任意时刻队列长度不超过 `Qc`。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素模拟的差分对照、重放确定性检查）
go test ./jit/

# 竞态检测 + 详细日志（打印输入、输出与判定依据）
go test -race -v ./jit/

# 指定用例
go test -run TestExampleWalkthrough ./jit/
go vet ./... && gofmt -l .
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
