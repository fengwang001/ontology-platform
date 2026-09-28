# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 滑动窗口最大值组件

包 `slidingwindow`（`slidingwindow/window.go`）提供固定容量的滑动窗口，
随元素进入与逐出实时维护窗口内最大值。

### 窗口与逐出规则

- 容量在 `New(capacity)` 时固定，容量必须为正整数，否则返回
  `slidingwindow.ErrInvalidCapacity`。
- `Append(v)` 追加一个元素；窗口已满时自动逐出**最早进入**的元素，并把该值
  通过返回值 `(evicted, true, nil)` 告知调用方。
- `Evict()` 显式逐出最早元素并返回其值；对空窗口逐出返回
  `slidingwindow.ErrEmptyWindow`，窗口状态不变。
- 值类型为 `float64`，只接受有限数值；`NaN`、`+Inf`、`-Inf` 返回
  `slidingwindow.ErrInvalidValue`，且拒绝的操作不改变任何内部状态。
- `BatchAppend(values)` 先整批校验，再依次写入；任一值非法则整批不生效
  （长度与搬运计数均不变）。

### 最大值维护规则

- 内部使用环形缓冲存放窗口元素，并叠加一个**值严格单调递减的双端队列**
  （队首即窗口最大值）。
- 新元素入队时，从队尾弹出所有**严格更小**的元素；相等的元素保留，保证
  并列最大值之一被逐出后，其余并列者仍能立即给出正确最大值。
- 队尾弹出计数通过 `Moves()` 暴露。每个元素一生至多从队尾被搬运（弹出）
  一次，因此插入的摊销开销为 O(1)。
- 双端队列节点直接以环形槽位索引串联；逐出最早元素时只做队首摘除，
  **不重扫整窗**，单次逐出为 O(1)。
- `Max()` 返回队首值；`Snapshot()` 在同一把读锁内返回窗口内容（旧→新）
  与最大值，二者逐一扫描必然一致，可被多 goroutine 并发读取。
- 组件不持有时间或随机状态，同一输入序列反复计算得到完全相同的输出。
- 每次追加/逐出都通过 `slog` 打印：输入值、逐出值（及是否逐出）、当前
  最大值、单调队列内容与判定依据（`basis`）。可用 `WithLogger(io.Writer)`
  重定向日志。

### 本地验证

```bash
# 组件测试（含并列逐出、单调递减、非法输入、批量原子性、并发一致性）
go test -race -v ./slidingwindow/

# 全量测试与静态检查
go test -race ./...
go vet ./...
gofmt -l .

# 可运行演示（打印每次判定的输入/逐出值/最大值/依据）
go run ./cmd/slidingwindow-demo
```

若 Go 构建缓存目录只读，可设置 `GOCACHE=/tmp/gocache`。

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
