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

## 周期任务触发协调器（`scheduler` 包）

`scheduler/coordinator.go` 实现多实例共享同一组周期时刻时的触发协调，
解决“谁在某个时刻执行、错过的时刻如何补偿、实例失效后会不会重做”。

### 序号计算

- 第 `k` 个时刻为 `t(k) = Anchor + k*Period`（`k` 从 0 起）。
- 实例在本地时钟 `now` 被唤醒时，不晚于 `now` 的最大序号：
  - `now < Anchor`：`m` 不存在，直接空转；
  - 否则 `m = (now - Anchor) / Period`（整数除法，自动处理时钟回拨）。
- `m` 不存在或 `m <= r` 时什么也不做，记录不变。

### 共享记录与推进/执行的先后

共享执行记录只有两栏：`LastSeq`（最后已推进序号 `r`，初始 `-1`）与
`WriteTerm`（写入它的任期）。唤醒处理顺序及理由：

1. 授权校验：任期必须由 `Grant` 授给该实例，否则整体拒绝；
2. 任期栅栏：`term < WriteTerm` 时整体拒绝（记录被更高任期写过）；
3. 计算 `m`，按策略对 `r+1..m` 生成处置计划（只决定，不写状态）；
4. 对计划逐项处理，**先在互斥锁内把记录原子推进到该序号（连同写入任期），
   再释放锁执行任务回调**：
   - 推进是“认领”。回调执行中途实例失效时，该序号已在记录中，
     任何实例（包括它自己重试）因 `seq <= r` 都不会再执行它，杜绝重做；
   - 每处理一个序号都在锁内复查 `WriteTerm`，一旦发现更高任期写入，
     旧任期实例立即放弃后续序号（执行序号严格递增的栅栏）；
   - 回调在锁外运行，唤醒可被多实例并发调用；推进的原子性保证每个序号
     全局至多执行一次，且按记录推进先后的执行序号严格递增。

被拒绝的操作（配置非法、任期不递增、未授权实例、旧任期）不产生任何状态变更。
`Grant` 要求任期严格大于已授出的最大任期。

### 三种补偿策略

| 策略 | 对 `r+1..m` 的处置 |
| --- | --- |
| `CatchUpAll` 全部补偿 | 较旧的 `r+1..m-K` 只推进不执行（`too-old`），最近的至多 `K` 个按升序执行 |
| `CatchUpOne` 补一次 | `r+1..m-1` 只推进不执行，只执行 `m` |
| `TolerantSkip` 容忍跳过 | `r+1..m-1` 只推进不执行；仅当 `now - t(m) < Tolerance` 时执行 `m`，否则不执行，但记录仍推进到 `m` |

容忍边界为严格小于：`lag == Tolerance` 即判超时；`Tolerance=0` 时只有恰好在
时刻点唤醒才执行。无论执行与否，记录都会推进到 `m`，保证后续唤醒不回补。

所有输入、输出与判定依据（含拒绝原因枚举）按发生顺序写入内存日志，
通过 `Coordinator.LogLines()` 读取；相同的授权、时钟与唤醒序列重放
得到相同的执行序列（无随机、无真实时钟依赖，时钟由 `Wakeup` 参数显式给出）。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -v ./scheduler

# 反复运行压测并发交错
go test -race -count=10 ./scheduler

# 覆盖率
go test -coverprofile=coverage.out ./scheduler
go tool cover -html=coverage.out
```

测试覆盖：配置与授权拒绝原因可区分、转移后旧任期实例唤醒（入口拒绝与
执行途中栅栏）、时钟回拨、前跳 100 个周期下三种策略的执行集合、
触发时刻与容忍严格边界、执行中失效不被任何实例重做、8 实例并发唤醒
（每序号恰好一次且执行顺序严格递增）、相同序列确定性重放，
日志断言输入/输出/判定依据字段齐全。
