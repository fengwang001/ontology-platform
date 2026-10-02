# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## watermark 包：滑动窗口窗格的输出时间戳合并与水位保持器

`watermark/` 实现滑动窗口聚合的输出水位推导。构造参数：窗口宽度 `S`、
滑动步长 `D`（`1 ≤ D ≤ S ≤ 16×D`，`S ≤ 10^9`）、输出时间戳策略
（`Earliest` / `Latest` / `End`）、允许迟到 `AL`（`0..10^9`）与窗格表容量
`Cap`（`1..10^6`）。

### 窗口分配与取整

窗口为 `[ws, ws+S)`，起点 `ws = k×D`（可为负），`end = ws+S`。元素 `ts`
属于所有满足 `ws ≤ ts < ws+S` 的窗口，即
`k` 从 `ceil((ts−S+1)/D)` 到 `floor(ts/D)`。取整按数学定义实现
（`floorDiv`/`ceilDiv` 对负数向 −∞/＋∞ 取整），向零截断会在 `ts < S−D`
时漏掉 `ws` 为负的窗口或多算窗口，必须避免。

### 保持值（hold）与三种输出时间戳策略

每个缓冲窗格的原始输出时间戳 `raw`：

- `Earliest`：窗格内最小元素时间戳 `min ts`；
- `Latest`：窗格内最大元素时间戳 `max ts`；
- `End`：恒为 `end−1`，不随元素变化。

保持值 `hold = max(raw, O)`，即钳制到当前输出水位 `O`：水位只进不退，
任何窗格的输出时间戳不得小于已发布的 `O`，因此 `hold` 始终不低于 `O`，
保证发出的每个 ON_TIME 窗格的输出时间戳都不小于发出之前的 `O`。

### Add 的三类判定（按 ws 升序）

- `I ≥ end+AL`：丢弃，丢弃计数加一；
- `end ≤ I < end+AL`：立即即发出迟到窗格 `(ws, 1, val, max(f, O))`，
  其中 `f` 对 `Earliest`/`Latest` 取 `ts`，对 `End` 取 `end−1`；
  迟到窗格不进窗格表，也不受容量限制；
- `I < end`：进入缓冲，累加 count/sum、更新 min/max ts 并重算 hold。

若缓冲类窗口中新建窗格数加窗格表现有大小超过 `Cap`，整个 `Add` 被拒绝
（不发出迟到窗格、不计丢弃、不改任何状态）。`Add` 是原子步骤，并发观察者
看不到只处理了一部分所属窗口的中间状态。

### Advance 与输出水位推导

`Advance(I′)` 要求 `I′ ≥ I`。窗格表中 `end ≤ I′` 的窗格按 `ws` 升序发出为
ON_TIME 窗格（输出时间戳即其 `hold`）并移除；之后
`O = max(O, min(I′, 剩余窗格 hold 的最小值))`，无剩余窗格时取 `I′`。
`I′ == I` 同样执行该步（hold 可能已被后续 `Add` 抬高，`O` 仍可上升）。
不变式：`O ≤ I`、`O` 单调不减、窗格表每个 `hold ≥ O`、表大小 `≤ Cap`、
同一窗口至多发出一个 ON_TIME 窗格。

最小 hold 用惰性删除的最小堆维护，非导出计数器 `holdProbes` 统计求最小
hold 时查看的堆项数（含被惰性丢弃的失效项），每次 `Advance` 的开销不超过
本次发出窗格数加 2 再加本次丢弃的失效项数，与窗格表大小无关。

### 错误与查询

拒绝原因可区分：`ErrInvalidParam`（参数越界）、`ErrCapacity`（容量不足）、
`ErrRegression`（水位回退）。`Add` 按参数非法→容量不足、`Advance` 按参数
非法→水位回退的顺序各只报第一个原因；被拒绝的操作不改变任何状态。
查询：`Output()` 返回 `O`，`Panes()` 按 `ws` 升序列出窗格表（含 hold），
`Dropped()` 返回丢弃计数。

### 本地验证

```bash
go test ./watermark/                 # 全部单测 + 2000 组随机序列对照朴素模拟
go test ./watermark/ -v -run TestNaiveComparison2000  # 打印输入/输出/判定依据
go test ./watermark/ -race           # 并发等价串行、不变式与竞态检测
go test ./watermark/ -v -run TestHoldProbesIndependentOfTableSize  # 计数器两档对照
```

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
