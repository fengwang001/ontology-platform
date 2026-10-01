# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

当前模块在根包提供遗嘱连接登记表，入口类型与错误为：

- `Will{Topic, Payload, DelayMillis}`：遗嘱主题、载荷和延迟毫秒数 `D`。
- `Registry.Connect(clientID, keepAliveSeconds, will, now)`：建立连接，`will` 为 `nil` 表示无遗嘱。
- `Registry.Activity(clientID, now)`：记录客户端活动。
- `Registry.Disconnect(clientID, normal, now)`：正常或异常断开。
- `Registry.Advance(now)`：执行入口处理并返回本次新发布的遗嘱。
- `Registry.Publications()`：读取确定性的累计发布记录，返回副本。

### 入口处理次序

除时钟倒退和参数非法的入口拒绝外，每个操作执行自身逻辑前，都会先以本次 `now` 做同一次处理：

1. 按客户端标识升序扫描全部在线连接。
2. 对保活秒数 `K > 0` 的连接，当且仅当 `now - 最近活动时刻 > 1500*K` 时判定异常断线。
3. 保活断线时刻取发现它的本次调用 `now`，不回推理论到期时刻。
4. 扫描期间产生的遗嘱加入等待队列，随后统一发布所有 `计划时刻 <= now` 的遗嘱。
5. 到期遗嘱按 `(计划时刻, 断线先后序)` 升序发布；即使 `D = 0`，也在同一次入口处理内发布。

连接建立本身记录一次活动。保活超时只由后续调用发现；已经被入口处理判为断线的客户端，再调用活动或断开会得到不在线错误，但其入口处理已经造成的断线和发布保留。

### 遗嘱规则

- 异常断线且有遗嘱时，计划时刻为“发现断线或显式异常断开的本次 `now` 加上 `D`”。
- 正常断开令当前连接携带的遗嘱作废，不进入等待队列。
- 同一客户端标识已在线时再连接属于接管：旧连接立即替换，旧连接携带的遗嘱作废，该标识尚在等待发布的遗嘱一并取消。
- 客户端已异常离线但遗嘱尚未到期时再连接，不属于“已在线接管”；尚未到期的等待遗嘱继续保留。
- 重连调用的 `now` 恰等于计划时刻时，入口处理会先发布旧遗嘱，之后才安装新连接，因此旧遗嘱不可取消。
- 每份等待遗嘱最多发布一次；发布记录包含客户端、主题、载荷和发布调用的 `now`。

### 拒绝与并发

拒绝原因按以下优先级返回：

1. `ErrClockRewound`：`now` 小于此前任一次调用传入的时间。
2. `ErrEmptyClientID`：客户端标识为空。
3. `ErrNegativeKeepAlive`：保活秒数为负。
4. `ErrNegativeDelay`：遗嘱延迟毫秒数为负。
5. `ErrClientNotOnline`：活动或断开时客户端不在线。

时钟倒退和参数非法不改变任何状态；不在线拒绝发生在入口处理之后，因此入口处理的副作用保留。所有方法通过同一把互斥锁串行化，查询返回载荷和发布记录副本，支持并发调用并可重复重放。

### 本地验证

```bash
# 拉取依赖
go mod tidy

# 编译根包
go build ./...

# 查看逐步模拟日志
go test -run TestNaiveStepSimulation -v ./...
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 查看逐步朴素模拟的输入、输出与判定依据
go test -run TestNaiveStepSimulation -v ./...

# 竞态验证
go test -race ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
