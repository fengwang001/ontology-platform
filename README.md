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

## 进程内事件总线（`eventbus` 包）

支持重入发布的进程内事件总线，按优先级调用主题处理器，派发过程中发布的
事件排队后广度优先处理。处理器调用序列、订阅变更的可见时点与错误记录可
精确复现。

### 核心规则

- **订阅**：`Subscribe(主题, 处理器标识, 优先级, 处理器)`。同主题处理器按
  优先级降序、同优先级按订阅先后升序调用。处理器内可发布、订阅、退订或
  调用停止传播。
- **发布与广度优先排队**：事件序号全局从 1 起递增。当前没有派发进行时，
  调用协程开始派发循环，循环取序号最小（队首）的待派发事件；队列排空后
  `Publish` 返回本次调用派发的事件数（含重入入队后被排空的事件）。已有
  派发进行（重入或来自其他协程）时，事件入全局 FIFO 队列并立即返回其
  序号，由正在派发的协程排空——广度优先而非递归嵌套。
- **快照时点**：每个事件在**开始派发的时刻**快照其处理器列表。快照中的
  处理器若在轮到它之前已被退订则跳过；派发中新增的订阅不加入当前事件，
  但对其后开始派发的事件可见。
- **停止传播**：`StopPropagation` 使当前事件不再调用其后的处理器，不
  影响队列中其他事件。在没有派发进行时调用会被拒绝。
- **错误记录**：处理器 panic 被捕获并记入错误记录（处理器标识，事件
  序号），可通过 `Errors()` 查询，不影响后续处理器与事件。
- **整体拒绝**（给出可区分的哨兵错误，且不改变订阅表、队列与事件序号
  计数）：主题为空串（`ErrEmptyTopic`）、同主题重复订阅同一处理器标识
  （`ErrDuplicateSubscription`）、退订不存在的订阅
  （`ErrSubscriptionNotFound`）、重入发布时待派发队列已达上限 Q
  （`ErrQueueFull`，Q 为 `NewBus(queueCap)` 的参数，不含正在派发的事件）、
  无派发时停止传播（`ErrNotDispatching`）。
- **并发与确定性**：所有方法可并发调用，处理器绝不在持锁状态下被调用；
  任何时刻最多一个协程在派发；相同的单协程调用序列重放得到完全相同的
  处理器调用序列与错误记录。

### 本地验证

```bash
# 全部单测（日志含每个用例的输入、输出与判定依据）
go test ./eventbus -v

# 竞态检测
go test ./eventbus -race

# 与朴素逐步模拟的对照用例
go test ./eventbus -run TestCompareWithNaiveModel -v
```
