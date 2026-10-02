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

## O(1) 风格单 CPU 调度器

调度器实现在 `scheduler` 包中。任务使用静态优先级 `sp=120+nice`，睡眠奖励与动态优先级为：

- 时间片：`sp < 120` 时 `ts(sp)=(140-sp)*20`，否则 `ts(sp)=max(5,(140-sp)*5)`。
- 睡眠量与奖励：`0 <= s <= 1000`，`bonus(s)=floor(s/100)`，范围 0 到 10。
- 动态优先级：`prio(sp,s)=min(139,max(100,sp-bonus(s)))`；`bonus >= 7` 为交互任务。

活动队列组和过期队列组各包含 prio 100 到 139 的 40 个 FIFO。dispatch 总是选择活动组中 prio 最小的非空队列；活动组为空且过期组非空时，整组互换并清零 `expired_ts`。

- 唤醒或新任务入队时，若当前任务为空则立即运行；若新任务 prio 严格更小则抢占。被抢占任务保留剩余时间片，回到自己 prio 队列的队首；prio 相等不抢占。
- 时间片到期时先重算 prio 和完整时间片，再计算 `starving = expired_count > 0 && now-expired_ts >= 100*nr`。非饥饿的交互任务回到活动组，其余任务进入过期组；过期组从空变为非空时记录 `expired_ts=now`。
- Sleep 记录 `sleep_start=now`，任务脱离所有队列并保留 prio 与时间片；Wake 累加 `now-sleep_start`，封顶 1000，再按新睡眠量重算 prio 并入队。
- Fork 只允许当前任务发起。子任务继承 nice/sp，`s_child=floor(s_parent/2)`，`ts_child=floor((ts_parent+1)/2)`，父任务保留 `floor(ts_parent/2)`。父时间片因此为 0（原时间片恰为 1）时，不推进时钟、不减少睡眠量，先按完整到期流程处理父任务；计算 `nr` 与 starving 时子任务尚未创建和入队，之后子任务才 arrive。

队列组用 64 位优先级位图和最小位缓存定位下一个队列；位图容量为 40 位，即最多 3 个 64 位字。生产实现不按任务数扫描队列，复杂度与排队任务数无关。

本地验证：

```bash
go test ./scheduler
go test -race ./scheduler
go test -run TestRandomizedAgainstNaiveSimulation -v ./scheduler
```

随机测试使用固定种子重放 2000 组操作序列，并与线性扫描 40 个队列、逐任务重算的朴素模型逐项比较。失败日志包含每一步输入、输出/错误和判定依据，便于精确复现。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
