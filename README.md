# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多服务台单队列仿真

`queue.NewSimulator(S)` 创建离散事件仿真器，`S` 必须在 `1..64`。所有时间均为非负整数，服务台编号为 `1..S`。

```go
sim, err := queue.NewSimulator(3)
err = sim.Add(id, arrive, service, patience)
err = sim.AdvanceTo(t)
outcome, err := sim.Outcome(id)
stats := sim.Stats()
```

`AdvanceTo(t)` 将已处理水位从 `H` 推到 `t`，只处理区间 `(H, t]` 内的事件时刻。每个事件时刻严格按以下顺序执行：

1. **完成与队列补位**：先释放本刻结束服务的服务台；然后按服务台编号升序，让 FIFO 公共队列队首开始服务，直到队列空或没有空闲台。
2. **到达**：本刻到达的顾客始终按 `id` 升序处理，与 `Add` 调用先后无关；若仍有空闲台，占用编号最小的空闲台，否则进入队尾。
3. **放弃**：最后按队列顺序移除所有 `arrive+patience <= 当前时刻` 的顾客。

边界规则：

- 顾客在 `start+service` 时刻完成；完成与开始服务可发生在同一整数时刻。
- 若队首顾客的 `arrive+patience` 恰等于服务台释放时刻，第一步先开始服务，因此该顾客算已服务，不会放弃。
- `patience=0` 且到达时没有空闲台，顾客会在第三步立即放弃，不会留在三步结束后的队列中，也不计入该刻最大队长。
- 最大队长只在每个有事件的时刻完成上述三步后记录；推进到没有事件的目标时刻不会虚构事件时刻。
- 拒绝操作返回带有可区分 `ErrorCode` 的 `*queue.OperationError`，且不会改变水位、队列、顾客状态或统计。

`Add`、`AdvanceTo`、`Outcome` 和 `Stats` 可并发调用；实现使用读写锁保证结果等价于某个串行执行顺序。把一次推进拆成任意非递减的 `AdvanceTo` 序列，与一次推进到最终时刻得到相同的逐顾客结果和统计。

### 本地验证

```bash
# 全量测试（随机对照测试会在 -v 日志中打印每组输入、输出和判定依据）
go test -v ./...

# 竞态检测
go test -race ./...

# 静态检查与格式
go vet ./...
gofmt -d .
```

测试包含 2000 组固定种子随机场景，将事件仿真器与“每个整数时刻都完整执行完成、补位、到达、放弃三步”的朴素实现逐字段对照。

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
