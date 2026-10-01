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

## 进程内事件总线

根包提供 `Bus`，通过 `New(Q)` 创建，`Q` 是已有派发进行时允许等待的最大事件数（不含正在派发的事件）。

### 调用规则

- `Subscribe(topic, handlerID, priority, handler)`：同主题按优先级降序调用；同优先级按订阅先后升序调用；同主题重复处理器标识返回 `ErrDuplicateSubscription`。
- `Unsubscribe(topic, handlerID)`：订阅不存在时返回 `ErrSubscriptionNotFound`；空主题返回 `ErrEmptyTopic`。
- `Publish(topic, payload)`：事件序号全局从 1 递增。没有派发时由当前调用者进入 FIFO 排空循环；已有派发时只入队并立即返回事件序号。
- `Event.StopPropagation()`：仅允许当前正在执行处理器的派发协程调用，停止当前事件后续处理器；`Bus.StopPropagation()` 在无派发上下文时返回 `ErrNoDispatch`。
- `Errors()`：返回复制出的 panic 记录，格式为处理器标识与事件序号；单个处理器 panic 不影响后续处理器、后续事件和事件序号。

### 一致性语义

- 每个待派发事件在“开始派发”的临界区内复制并排序处理器列表；派发期间新增订阅不会进入当前事件，但会影响之后开始派发的事件。
- 快照中的处理器在真正调用前仍会复核订阅身份；快照后退订的处理器会跳过，退订后以相同标识重新订阅也不会误调用旧快照。
- 处理器中发布的事件进入全局 FIFO 队尾，因此当前事件的全部未跳过处理器先执行，之后才处理重入事件（广度优先，不递归嵌套）。
- 当派发中且等待队列长度已达到 `Q` 时，发布返回 `ErrQueueFull`；该拒绝以及所有其他拒绝都不会修改订阅表、队列或事件序号。
- 所有共享状态由互斥锁保护，处理器一律在锁外调用；任意时刻最多一个协程执行派发排空循环。

### 本地验证

```bash
# 普通测试；测试日志包含输入、输出和与逐步朴素模拟一致的判定依据
go test -v ./...

# 并发验证
go test -race ./...

# 如果环境的 GOCACHE 默认目录只读，可显式使用 /tmp
GOCACHE=/tmp/go-build-cache go test -race -v ./...
```
