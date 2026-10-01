# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 网卡式中断节流合并器

实现位于 `interrupt` 包，由 `New(gap, threshold)` 创建合并器：

- `gap >= 0` 是两次中断之间的最小间隔；`threshold >= 1` 是累计事件门限。
- 内部维护待处理事件数 `pending`、上次触发时刻、等待确认标志和屏蔽标志；所有操作与查询均由互斥锁保护，等价于某个合法的串行顺序。
- 可触发条件为：有事件、未等待确认、未屏蔽，并且当前时刻不早于 `last + gap`，或者待处理事件数已达到门限。从未触发时，`gap` 不限制首次触发。
- 触发时记录触发时刻和当时携带的全部事件数，然后清空待处理事件、更新上次触发时刻并进入等待确认。

每个成功操作都严格按三步处理：

1. 补触发：只有此前已经触发过、有待处理事件、未等待确认、未屏蔽、事件数低于门限且 `last + gap <= t` 时执行；触发时刻取 `last + gap`，不是操作时刻 `t`。
2. 施加操作：`Event(t,n)` 增加事件；`Ack(t)` 清除等待确认；`Mask(t)` 与 `Unmask(t)` 切换屏蔽；`Tick(t)` 不改变状态。
3. 当前时刻判定：在时刻 `t` 重新检查完整触发条件；触发时刻取 `t`。

因此，`Mask(t)` 与到点补触发同刻时会先在 `last + gap` 触发，再置屏蔽；`Ack(t)` 或 `Unmask(t)` 若让已累积事件满足条件，则在 `t` 立即触发。操作时刻必须非递减；非法构造、非法事件数、等待标志为假时的 Ack 均返回独立哨兵错误。被拒绝的操作不会改变状态，也不会执行补触发。

账目不变量为：所有中断携带事件数之和加当前 `pending`，等于成功 `Event` 的事件数总和；触发次数减去成功 Ack 次数始终为 0 或 1；触发时刻非递减；相同操作序列重放得到相同记录。

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

# 中断合并器：详细日志包含输入、输出、记录与三步判定依据
go test -v ./interrupt

# 并发串行化验证
go test -race ./interrupt

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
