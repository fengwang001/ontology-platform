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

## CFS 带宽控制器

`cfs` 包实现多 CPU 的配额、周期与节流模型。构造函数：

```go
controller, err := cfs.New(Q, P, S, B, C)
```

- `Q`：每周期全局配额；`P`：周期长度；`S`：一次借用片；三者范围均为 `1..10^9`。
- `B`：全局池可结转的最大突发余量，范围为 `0..10^9`，全局池上限为 `Q+B`。
- `C`：CPU 数，范围为 `1..64`。配置越界返回 `cfs.ErrInvalidConfig`。

操作包括 `Wake(now, cpu)`、`Run(now, cpu, d)` 与 `Idle(now, cpu)`；查询包括 `Stats()`、`State(cpu)`、`Pool()` 与 `Queue()`。

### 借用与节流

初始全局池 `G=Q`，所有 CPU 均为空闲且本地余量 `l=0`。`Run` 先全额扣除 `d`，然后在 `l<=0` 时申请：

```text
want = S-l
take = min(want, G)
l += take
G -= take
```

因此 `l` 为负时会多借，用于先补齐缺口再拿到借用片。扣除并借用后只有 `l<=0` 才节流；`l` 恰为 `0` 也节流。被节流 CPU 记录 `since=now` 并追加到 FIFO 队尾。

### 周期补给

每个操作先处理所有 `kP<=now` 的未处理边界，`kP==now` 也先于操作。处理边界时：

```text
G = min(Q+B, G+Q)
need = 1-l
take = min(need, G)
```

队列严格按进入先后补给，而不是按 CPU 编号。CPU 得到额度后若 `l>0`，立即变为运行、出队，并累计 `boundary-since` 到节流时长。若队首在本边界只能部分补给，则 `G=0`，本边界后续 CPU 完全得不到额度。

`B=0` 时每周期等价把全局池重置为 `Q`；`B>0` 时上一周期剩余额度可结转，但池永远不超过 `Q+B`。

### 空闲归还

`Idle` 只接受运行中的 CPU。若 `l>1`，归还 `slack=l-1`：

```text
G = min(Q+B, G+slack)
l = 1
```

若 `l==1`，不归还；CPU 随后变为空闲。再次 `Wake` 时保留该本地余量。

### 原子性与拒绝

所有状态修改在互斥锁内完成，查询使用读锁，结果等价于某个串行顺序，观察者不会看到只补给了一部分节流 CPU 的中间状态。

错误按以下顺序只返回第一个：CPU 越界、参数非法、时间回退、边界处理后的状态错误。被拒绝的操作先在副本上推演边界和操作；任一检查失败就丢弃副本，因此不会提前提交周期、全局池、队列、CPU 余量、统计或 `lastNow`。

### 复杂度

包内有非导出计数器 `boundaryIters`，用于测试验证大跨度性能。空队列的连续周期用一次算术完成；队列非空时，只快进队首仍无法解除节流的整周期，到达解除边界后恢复逐 CPU 补给。`k*Q` 使用封顶算术，避免乘法溢出。

在 `P=1,Q=1,B=0`、CPU 欠下约 1000 单位后，`Run` 到 `now=10^15` 的 `boundaryIters` 不超过 `1010`。

### 本地验证

```bash
GOCACHE=/tmp/go-build-cache /usr/local/go/bin/go test ./cfs -v
GOCACHE=/tmp/go-build-cache /usr/local/go/bin/go test -race ./cfs
GOCACHE=/tmp/go-build-cache /usr/local/go/bin/go test ./...
```

测试覆盖题目示例、差一节流、同刻边界、FIFO 部分补给、空闲归还、突发封顶、拒绝回滚、错误优先级、大跨度防溢出、大跨度 `boundaryIters`、并发访问，以及 2000 组随机序列与逐边界逐 CPU 朴素模型对照。随机测试日志会打印种子、配置、输入、输出和接受或拒绝的判定依据。
