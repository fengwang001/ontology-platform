# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 滑动窗口最大值组件（`slidingwindow` 包）

固定容量滑动窗口，随元素进入/逐出实时维护窗口内最大值，单次操作均摊 O(1)，支持并发读取。

### 维护规则

- **窗口**：容量在构造时固定（`New(capacity)`），按进入顺序存储元素，实现位于 `slidingwindow/window.go`。
- **追加与自动逐出**：`Append(v)` / `AppendBatch(vs)` 追加元素；窗口已满时每进入一个新元素就自动逐出最早元素，自动逐出的值随返回值给出。
- **显式逐出**：`Evict()` 返回并移除最早进入窗口的元素。
- **最大值**：`Max()` 返回窗口内所有元素的最大者；`Snapshot()` 在同一把读锁下同时返回窗口内容副本与最大值，因此两者永远对应同一时刻。

### 数据结构与复杂度判定

- 内部为两个定长环形缓冲：元素环（保存窗口内容）+ **单调非递增双端队列**（队首即最大值）。
- 入队时仅把队尾**严格更小**的元素挤出；相等元素保留（旧的在前）。因此并列最大值之一被逐出时，后面的并列值自然接管，最大值在并列与逐出场始终正确。
- 每个元素在队列尾部至多被挤出（搬运）一次，累计搬运次数由 `MoveCount()` 暴露，恒不超过累计进入元素数——这是均摊 O(1) 的判定依据。
- 逐出最早元素时只需比较其序号与队首序号：相等则摘队首，否则它早已被挤出；**不重扫整窗**。

### 非法操作（可区分的拒绝原因）

| 场景 | 返回错误 |
| --- | --- |
| 容量非正（0 或负数） | `ErrInvalidCapacity` |
| 对空窗口 `Evict()` / `Max()` / `Snapshot()` | `ErrEmptyWindow` |
| 写入 `NaN` | `ErrInvalidValue` |

- 被拒绝的操作不改动内部结构，`MoveCount()` 也不增加。
- `AppendBatch` 先全量校验再加锁写入：任一值非法则整批不生效（含自动逐出也不发生）；空切片为无操作成功。
- 确定性：实现无 goroutine、无 map 迭代，同一输入序列反复计算得到完全相同的逐出序列、最大值序列与搬运计数。

### 本地验证

```bash
# 单元测试（带竞态检测与输入/逐出/最大值/判定依据日志）
go test -race -v ./slidingwindow/

# 覆盖率
go test -cover ./slidingwindow/

# 全量检查
gofmt -l .
go vet ./...
go test -race ./...
```

覆盖用例：并列最大值维持与逐个逐出、单调递减段（摘队首路径）、单调递增段（尾部搬运路径）、批量超容量逐出、各类非法输入与拒绝后结构不变、搬运计数上界、并发读一致性（8 读协程 × 20000 次与扫描结果比对）、同序列 21 次重复的确定性。

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
