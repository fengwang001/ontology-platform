# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## CFS 带宽控制器（`./cfs`）

多 CPU 的 CFS 带宽控制器：配额与周期节流。构造参数为每周期配额 `Q`、
周期 `P`、借用片 `S`（均为 1 到 10^9）、突发上限 `B`（0 到 10^9）与
CPU 数 `C`（1 到 64），任一越界即以 `ErrInvalidConfig` 整体拒绝。

### 借用片与节流条件

- 每个 CPU 有本地余量 `l`（可为负，初值 0）与状态（空闲、运行、节流）。
- `Run(now, cpu, d)` 全额消耗：`l -= d`；若 `l <= 0`，按借用片申请
  `want = S - l`（`l` 为负时借得更多），`take = min(want, G)` 从全局池借入。
- 借到后 `l` 仍不大于 0（`l` 恰为 0 也算）则该 CPU 变为节流，
  记录起点 `since = now`，进入节流队列队尾，`nThrottled` 加一。

### 边界补给（突发结转与 Q+B 封顶）

- 周期边界为 `P、2P、3P…`，每个操作生效前先处理所有 `k*P <= now` 的
  未处理边界（恰等于 `now` 的边界也先处理）。
- 处理边界 `b`：`periods` 加一，`G = min(Q+B, G+Q)`——上一周期剩余额度
  最多结转到 `Q+B`（`B=0` 时即重置为 `Q`；`B>0` 时余量可结转，例如
  `Q=10、B=15、G=3` 的下一边界后为 13 而非 10）。
- 然后按节流队列先后（进入先后，而非 CPU 编号）逐个补给：
  `need = 1 - l`，`take = min(need, G)`；补给后 `l > 0` 则解除节流
  （`l` 恰为 1），`throttledTime += b - since` 并出队；`take < need`
  时 `G` 已为 0，队列中其余 CPU 完全得不到额度并保持节流。

### 空闲归还

`Idle(now, cpu)` 运行变空闲；若 `l > 1`，归还 `slack = l - 1`，
`G = min(Q+B, G+slack)`（同样受 `Q+B` 封顶），`l` 变为 1；`l <= 1` 不归还。

### 拒绝与副本推演

错误按序只报第一个，可用 `errors.Is` 区分：
`ErrCPUOutOfRange`（cpu 越界）→ `ErrInvalidParam`（now 不在 0 到 10^15，
或 `d` 不在 1 到 10^6）→ `ErrTimeRegression`（now 小于 lastNow）→
状态类（`ErrNotIdle` / `ErrNotRunning` / `ErrThrottled`）。
状态类判定针对边界处理之后的状态；实现先在副本上推演边界与操作，
接受后才提交，被拒绝的操作不改变任何状态（含边界、统计与 `lastNow`）。

### 复杂度计数器

跨越 `k` 个边界时，只有节流队列非空的边界才逐个处理（计入非导出计数器
`boundaryIters`），队列空了之后剩余的 `k` 个边界以算术一次完成
（`periods += k`，`G = min(Q+B, G+k*Q)`，`k` 可达 10^15，乘积防溢出）。
`P=1、Q=1、B=0` 欠 1000 单位后在 `now=10^15` 处 `Run`，
`boundaryIters` 不超过 1010。

### 并发与确定性

所有操作与查询可并发调用（内部互斥锁），边界处理与补给是一个原子步骤，
任何观察者看不到只补给了一部分节流 CPU 的中间状态；相同的操作序列重放
得到完全相同的状态与统计。

### 本地验证

```bash
# 全部测试（含两个规格示例、定向用例、2000 组随机序列对照朴素模拟）
go test ./cfs/

# 查看随机对照日志（输入、输出与判定依据）
go test ./cfs/ -run TestRandomAgainstNaive -v

# 竞态检测
go test -race ./cfs/
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
