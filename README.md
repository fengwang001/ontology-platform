# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 混合关键级运行时模式监控器

核心实现位于 `mcsched` 包，用于单核固定优先级调度下的 LO/HI 模式监控。

### 每个时刻的固定次序

`Step(n)` 以时刻 `t=CurrentTime()` 为起点处理 n 个 tick；`RunAt(0)` 对应从时刻 0 开始执行的第一个 tick。每个时刻严格按以下顺序处理：

1. 截止期错过：遍历所有任务，未完成且截止期 `d=t` 的作业立即移除，按 LO/HI 分别计数。
2. 模式恢复：当前为 HI 且截止期处理后没有任何未完成作业时，在释放新作业前恢复到 LO，并增加恢复计数。
3. 作业释放：满足 `t >= φ` 且 `(t-φ)%T == 0` 的任务释放第 `(t-φ)/T` 个作业，截止期为 `t+T`。HI 模式下 LO 任务的释放被跳过，跳过计数加一，作业序号仍然占用。
4. 执行一个 tick：在未完成作业中选择 `Prio` 最小者执行；空闲 tick 不运行任务。
5. tick 末判定：若作业剩余量为 0，则完成并移除；否则当模式仍为 LO、任务为 HI、已执行量恰好等于 `CL` 时，在该 tick 末切换到 HI。

### 切换、恢复与计数边界

- 实际执行量恰好等于 `CL` 且作业在该 tick 完成时，不切换；实际执行量大于 `CL` 时，已执行量第一次恰等于 `CL` 且作业未完成的 tick 末才切换。
- 切换到 HI 时舍弃所有当前未完成 LO 作业并按个数增加舍弃计数；同一 tick 早些时候释放的 LO 作业也会立即被舍弃。
- 舍弃发生在 tick 末，跳过发生在释放阶段；被舍弃或跳过的作业既不计完成，也不计错过。
- 只有 HI 模式且先经过截止期错过处理后系统无未完成作业，才在释放前恢复 LO；因此同一时刻新释放的作业会看到 LO 模式。
- HI 作业无论当前模式为何，只要在截止期时刻仍未完成都计入 HI 错过。
- 重复 `SetDemand` 同一作业以最后一次为准；若释放时刻已经早于当前时刻则拒绝，释放时刻等于当前时刻仍可设置。
- `AddTask` 的拒绝顺序为：已开始、参数非法、编号重复、优先级重复、任务数已满；`SetDemand` 的拒绝顺序为：编号不存在、参数非法、作业已释放。被拒绝操作不会改变任何状态。
- 所有公开操作由同一个互斥保护，可并发调用，结果等价于某种合法串行顺序；`Step(a+b)` 与连续调用 `Step(a)`、`Step(b)` 的轨迹逐位相同。

### API 与轨迹查询

- `NewMonitor()` 创建初始 LO 模式监控器。
- `AddTask(Task)` 增加任务，任务数上限为 16。
- `SetDemand(id, k, c)` 覆盖第 k 个作业的实际执行量；默认执行量为 `CL`。
- `Step(n)` 推进 1 到 1,000,000 个 tick。
- `Mode()` 查询当前模式，`Stats()` 查询七项计数与完成数，`RunAt(tick)` 查询第 tick 个 tick 运行的任务编号；空闲返回 `("", false)`。

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

# 只运行混合关键级监控器测试；-v 会打印 2000 组随机对拍的输入、输出和判定依据
go test -v ./mcsched

# 对拍覆盖示例、边界、拆分 Step 等价性与并发安全
go test -race ./mcsched

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
