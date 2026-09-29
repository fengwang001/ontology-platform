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

## 按谓词条件的增量订阅

`subscription.Pusher[K]` 为变更维护从 1 开始的全局连续序号。每次 `Push(key)` 都先在同一临界区内分配序号、判定所有当前活跃订阅、完成投递并更新位点，因此并发推送后全局最终位点等于成功推送次数，序号无洞。

### 增量语义

- `Register(id, lowerBound, updates)` 在当前全局位点生效，记录 `StartAfter`。
- 只有满足 `change.Sequence > StartAfter` 的后续变更参与该订阅判定，注册之前已经分配或投递的历史变更不会补推。
- 命中条件为 `change.Key >= lowerBound`，键恰好等于下界时必须命中。
- `Unsubscribe(id)` 返回后该标识立即从活跃集合移除，后续变更零推送；同名标识重新注册会从新的当前位点开始。
- `Snapshot()` 返回全局位点和活跃订阅的 `StartAfter`、`DeliveredThrough` 副本，可与推送并发读取；`DeliveredThrough(id)` 可并发查询单个订阅位点。
- 调用方提供的 channel 必须有足够缓冲或持续消费；推送在临界区内写入 channel，满 channel 会形成反压。

### 错误契约

失败操作不会插入、删除或修改任何状态，且三个错误可通过哨兵值直接判定：

- `ErrInvalidSubscriptionID`：订阅标识为空或仅包含空白，或注册 channel 为 `nil`。
- `ErrDuplicateSubscription`：活跃订阅中已存在相同标识。
- `ErrSubscriptionNotFound`：退订的标识当前不存在。

### 本地验证

```bash
# 全量测试
go test ./...

# 并发与数据竞争验证
go test -race -v ./subscription

# 静态检查
go vet ./...

# 若系统 PATH 未包含 Go，可显式使用 /usr/local/go/bin/go
GOCACHE=/tmp/ontology-go-cache GOMODCACHE=/tmp/ontology-go-modcache \
  /usr/local/go/bin/go test -race -v ./subscription
```

测试日志通过 `slog` 记录输入键、订阅下界、序号、推送结果以及每个订阅的判定依据（命中、低于下界或注册晚于变更）。`go test -v` 会打印这些日志，便于复现边界与增量行为。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
