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

## 注入时钟事件循环

根包提供可复现的浏览器事件循环内核：

- `NewLoop(now, Config)` 创建注入时钟；`Config` 可配置帧间隔、饥饿上限、最小定时器延迟、嵌套阈值与钳制延迟。
- `EnqueueTask`、`EnqueueMicrotask`、`RequestFrame`、`RequestIdle`、`SetTimeout` 返回可取消 `Handle`。
- `MarkDirty` 标脏文档；`Advance(target)` 是唯一推进时钟的操作，并返回本次推进产生的 `TraceEvent`。
- 回调中的 `Context` 可继续注册任务、微任务、动画帧、空闲回调和定时器。
- `Cancel(handle)` 可取消尚未执行的任务、微任务、帧回调、空闲回调或未到期定时器。
- `Errors(from, to)` 按回调抛出时刻区间查询错误报告；错误次序等于抛出次序。
- 错误哨兵为 `ErrInvalidArgument`、`ErrClockRollback`、`ErrUnknownHandle`、`ErrDuplicateCancel`，可用 `errors.Is` 区分。

关键规则：

- 五种任务源源内严格 FIFO；用户交互源普通情况下优先，其他源达到饥饿上限后强制选择。
- 微任务在当前任务结束后、下一任务/渲染/空闲前全部清空，嵌套微任务同批执行。
- 渲染按帧网格触发；连续错过多个边界只补一次，无请求边界不计错过。
- 空闲回调只在无任务、无微任务、无待渲染且剩余时间严格大于 0 时执行。
- 空闲超时在到期时转为内部源任务，且只执行一次。
- 定时器执行最小延迟和严格超过嵌套深度阈值后的钳制；定时器任务入队时刻为到期时刻。

设计取舍见 `DESIGN.md`。随机操作序列会与测试内独立实现的朴素模型对拍；设置 logger 后可记录每次推进输入、轨迹和判定依据：

```go
loop.SetLogger(testLogger{})
events, err := loop.Advance(100)
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
