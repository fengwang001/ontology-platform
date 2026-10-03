# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 单核过载接纳控制器（`admission` 包）

`admission.Controller` 实现一个带容忍延迟与价值驱逐的单核 EDF 接纳控制器，
所有方法（`Submit` / `Advance` / `Pending` / `Value` / `Evicted` / `Now`）
内部用互斥锁串行化，可被并发调用，结果等价于某个串行顺序。

### 作业与时钟

- 作业：编号（非空、不超过 32 字节）、到达时刻 `now`、执行量 `C`（1..10^6）、
  截止期 `d`（0..10^15）、容忍延迟 `M`（0..10^6）、价值 `v`（1..10^6）。
- 时钟单调不减，初值 0。
- 待处理作业始终按 `(d, 编号字节序)` 升序排列（EDF 序），从当前时钟起连续运行。
- 第 i 个作业的完成时刻 `f_i = 当前时钟 + 它与它之前所有作业的剩余量之和`。

### 可行性判定

从当前时钟出发，按 EDF 序**恰好扫描一遍**待处理作业，累加剩余量得到每个
`f_i`。可行当且仅当每个作业都满足 `f_i - d_i <= M_i`；恰等通过，大 1 即不可行。
`f_i < d_i` 时延迟按 0 计。任何时刻（接纳或驱逐后）待处理集合都保持可行。

### 价值结算

作业在实际完成时刻 `f` 结算延迟折价价值：

```
value += floor(v * (M + 1 - max(0, f - d)) / (M + 1))
```

准时（`f <= d`）得全额 `v`；容忍边界 `f-d = M` 得 `floor(v/(M+1))`；
`int64` 下乘法最大约 10^12，不溢出。被驱逐的作业不结算。总价值只增不减。

### 价值密度与驱逐次序

接纳导致不可行时，只在**未开始**（从未运行过 1 个单位）作业中反复驱逐。
一旦作业运行过哪怕 1 个单位即永久标记已开始，即使被抢占也不可驱逐。

- 价值密度 = `v / 剩余量`，更小者先出。
- 不做浮点除法，用交叉相乘比较：`v1*r2 < v2*r1`（乘积 <= 10^12，不溢出）。
- 密度并列时取编号字节序**大**者先出。
- 每驱逐一个立即重判可行性，一旦可行即停止。
- 若新作业自己被选中，或已无未开始者可驱逐而仍不可行：拒绝新作业，
  **本轮第四步驱逐过的所有作业一律恢复**，它们不进入驱逐记录。

### 拒绝原因（按此顺序只报第一个）

1. `RejectInvalidArgs`：参数非法（含 `Advance` 时钟超出 0..10^15）。
2. `RejectClockBack`：时钟回退。
3. `RejectDuplicateID`：推进后编号仍存在于待处理集合（已完成者编号可复用）。
4. `RejectInfeasible`：`now + C > d + M`，独占处理器也无法完成。
5. `RejectOverload`：经过驱逐流程仍无法接纳。

被拒绝的操作不改变时钟、待处理集合、总价值与驱逐记录。`Submit` 中推进到
`now` 所发生的运行与结算同样先做完整快照，拒绝时整体回滚，由后续成功操作补做。
接纳时本轮驱逐的作业按驱逐先后追加到 `Evicted()`。

### 复杂度与非导出计数器

驱逐循环中不重新排序（新作业以二分定位插入 EDF 序，一次 `Submit` 的排序
次数为 0，断言上界 <= 1）；可行性判定次数 = 驱逐个数 + 1，每次判定恰好
扫描当时待处理序一遍。测试通过非导出的 `lastSubmitCounters`（同包测试）
断言 `sorts <= 1`、`scans == 驱逐数 + 1`。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测
go test -race ./...

# 查看定向用例与 2000 组随机对拍的输入/输出/判定依据日志
go test ./admission -run TestRandomDifferential2000 -v

# 代码检查
gofmt -l .
go vet ./...
```

`naive_fuzz_test.go` 内置一份按题面逐单位（每个时钟单位执行 EDF 队首
1 个单位）实现的独立朴素模拟器，对 2000 组随机 `Submit`/`Advance` 序列，
逐步比对接纳结果、拒绝原因、驱逐序、`Pending`、`Value`、时钟，任何分歧
都打印该组完整输入、输出与判定依据。

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
