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

## 翻滚窗口计数器（`window` 包）

`window.Counter` 是一个带**水位线（watermark）**与**迟到容限（allowed lateness）**的
按键（key）翻滚窗口计数器，并发安全、输出确定可复现。

### 窗口划分

- 窗口大小 `WindowSize > 0`，固定、不重叠、**左闭右开**：`[n*size, (n+1)*size)`。
- 事件时间可正可负。起点用余数法向负无穷取整，例如 `size=10` 时：
  `t=-1 → [-10,0)`、`t=-10 → [-10,0)`、`t=0 → [0,10)`、`t=10 → [10,20)`。
- 每个键的窗口相互独立，状态按 `(key, windowStart)` 保存。

### 水位线

- 水位线 = 见过的**最大事件时间** − `WatermarkDelay`（延迟必须 `>= 0`）。
- 水位线只随最大事件时间**单调前进**，更旧的事件不会让它回退；初始为 `math.MinInt64`。
- 当窗口结束时间 `end <= watermark` 时窗口**触发**，为该键该窗口产出一条
  `ResultTriggered` 计数。同一次推进触发多个窗口时，按 `(end, key)` 升序依次输出。

### 迟到、修正与清除

- 窗口触发后不会立刻删除：在 `watermark <= end + AllowedLateness` 期间仍然保留。
- 此期间到达的迟到事件被**接受**，计数累加，并产出一条 `ResultCorrected`
  （含最新累计计数），用于修正此前已输出的值。
- **边界判定**：`watermark == end + AllowedLateness` 仍算容限内（接受/修正）；
  当水位线**严格越过** `end + AllowedLateness` 时窗口才被**清除**。
- 窗口清除后再到达的同窗口事件一律**丢弃**（`OutcomeDropped`）并计入
  `DroppedEvents`，不会重建已关闭窗口。
- 每条输出带单调递增的 `Seq`（从 1 开始），同一输入序列反复计算输出完全一致。

### 非法输入与整体拒绝

下列情况在任何状态改变之前被**整体拒绝**，返回可 `errors.Is` 区分的错误，
且不改变水位线、丢弃数与任何已产生输出，计数器之后仍可继续正常使用：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidWindowSize` | 窗口大小非正 |
| `ErrInvalidDelay` | 水位线延迟为负 |
| `ErrInvalidLateness` | 迟到容限为负 |
| `ErrInvalidWindowLimit` | 未结算窗口上限非正 |
| `ErrEmptyKey` | 键为空串 |
| `ErrTooManyOpenWindows` | 同时保留的未结算窗口数达到 `MaxOpenWindows` 上限 |

注意：迟到超容限的丢弃不是错误，`Add` 返回 `(OutcomeDropped, nil)`。

### 并发与可复现

- 内部用 `sync.RWMutex` 保护；`Results` / `ActiveWindows` 返回的都是拷贝
  （`ActiveWindows` 按 `(WindowStart, Key)` 排序），多个并发只读得到逐字段一致的快照。
- 处理顺序固定（校验 → 定位窗口 → 推进水位线 → 接受/丢弃 → 触发 → 清除），
  因此同一输入序列的结果、序号、丢弃数与最终保留窗口都可复现。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 查看窗口包的详细用例与判定日志
go test -race -v ./window

# 运行演示：打印每条输入、触发/修正/清除/丢弃日志与最终结果
go run ./cmd/window-demo
```

