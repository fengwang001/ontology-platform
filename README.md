# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 流与版本表的时态连接（`temporaljoin`）

`temporaljoin` 包把**事件流**连接到**版本表**：每个事件都被连接到其事件时间
（event time）那一刻生效的版本。即使版本变更晚于事件到达，只要它在水位线关闭
对应时间点之前到达，结果就始终正确；被接受的事件在并发下恰好输出一次，且结果
与对版本表的朴素时点查询完全一致。

### 版本区间语义

- 每个键的版本按**生效起点** `EffectiveAt` 升序排列。
- 生效区间**左闭右开**：版本 `vᵢ` 在 `[vᵢ.EffectiveAt, vᵢ₊₁.EffectiveAt)`
  内生效；**最后一个版本生效到正无穷**。
- **墓碑（tombstone）** 是一种特殊版本，表示该键在其区间内无值（用于“删除”）。
- 同一生效起点重复写入时**后写覆盖先写**（值覆盖值、值覆盖墓碑、墓碑覆盖值）。

### 查询规则

对事件 `(key, eventTime)`：

1. 在该键的版本中，取 `EffectiveAt <= eventTime` 且生效起点**最大**的版本
   （因此生效起点恰好等于事件时间时命中该版本，左闭）。
2. 不存在这样的版本 → 未命中，判定依据 `no_version`。
3. 命中版本是墓碑 → 未命中，判定依据 `tombstone`。
4. 否则命中其值，判定依据 `hit`。

事件先进入有界缓冲；推进水位线时，所有 `eventTime <= watermark` 的缓冲事件被
连接并输出，输出按 **(事件时间, 接受序号)** 升序，每个被接受事件**恰好一次**。

### 迟到与拒绝规则

水位线单调非降，初始为 `-∞`。下列操作被拒绝并返回带**可区分原因**的
`*RejectError`，且拒绝不改变版本表、水位线、缓冲或任何已输出结果（先校验、后写入）：

| 原因常量 | 触发条件 |
| --- | --- |
| `empty_key` | 事件或版本变更的键为空字符串 |
| `late_version_change` | 版本变更生效起点 `<= 当前水位线`（等于也算迟到，区间已关闭） |
| `late_event` | 事件时间 `<= 当前水位线` |
| `watermark_regression` | 新水位线 `< 当前水位线`（相等是无害空操作，不是回退） |
| `buffer_overflow` | 接受该事件会使缓冲超过 `BufferCapacity` |
| `invalid_config` | `BufferCapacity <= 0` |

关键推论：因为版本只需“**晚于水位线**”到达即可，所以事件可以先于它要连接的
版本进入缓冲，版本也可以乱序（按生效起点）到达，只要都发生在水位线越过对应
时间点之前。

### 日志

组件通过 `slog` 记录每次输入与判定：`version accepted`（含起点/墓碑）、
`event accepted`（含序号/事件时间）、`event joined`（含 hit、判定依据
`basis`、命中版本起点与值）、`operation rejected`（含原因与细节）。
可在 `Options.Logger` 注入自定义 logger。

### 快速示例

```go
j, _ := temporaljoin.NewJoiner(temporaljoin.Options{BufferCapacity: 1024})

j.PutVersion("k", 1, []byte("v1")) // [1,2) -> "v1"
j.PutTombstone("k", 2)             // [2,5) -> 无值
j.PutVersion("k", 5, []byte("v5")) // [5,∞) -> "v5"

j.PutEvent("k", 1, nil) // 先缓冲
j.PutEvent("k", 2, nil)
results, _ := j.AdvanceWatermark(5) // 水位线推进时连接输出
// event@1 -> hit "v1"；event@2 -> tombstone 未命中
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 本地验证

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出（推荐）
go test -race -v ./...

# 高频重复并发用例，排查偶发问题
go test -race -count=20 ./temporaljoin

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 代码检查
gofmt -l .
go vet ./...
```

若 `go` 不在 PATH（如安装在 `/usr/local/go`）：

```bash
export PATH=$PATH:/usr/local/go/bin
```
