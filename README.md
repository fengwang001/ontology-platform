# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 快照与增量日志衔接组件（`snapshot` 包）

在源表不停写入的前提下，对**键范围**先做一致性快照，再补上快照期间的增量日志，
使下游视图始终可追平源表，且输出**不重复、不回退**。

### 数据模型

- `Source`：并发安全源表。每次 `Put`/`Delete` 都在同一把锁内完成
  “改表 + 追加日志 + 推进序号”。日志序号从 1 开始，连续递增、不跳号；
  `LastSeq()` 为当前最后序号（无写入时为 0）。
- `Syncer`：维护下游视图与每个键范围的处理状态。

### 三步处理流程（每个键范围严格按序）

1. **记低水位** `BeginRange(start, end)`：
   - 校验范围合法（闭区间，`start <= end`）且与已有范围（进行中或已完成）
     **不相交**（端点相接即算相交；`[a,b]` 与 `[b+1,c]` 不算）；
   - 记录低水位 `lowSeq = Source.LastSeq()`。
2. **读快照** `SnapshotRange(start, end)`：在源表读锁内取该范围某一原子时刻的
   键值行。快照时刻可能晚于低水位，其前后的写入差异交给下一步修正。
3. **记高水位并修正** `CompleteRange(start, end)`：
   - 在源表一次加锁内原子取高水位 `highSeq = LastSeq()` 与日志切片；
   - 把序号满足 `lowSeq < seq <= highSeq`、且**键落在闭区间 `[start,end]`**
     的日志严格按序号顺序应用到快照（Put 覆盖、Delete 移除）；
   - 通过行数上限校验后，才把结果原子安装到视图，并把高水位作为该范围的
     **轮询处理位置**。校验失败则范围停留在快照阶段、不安装任何行、不记高水位，
     可在源表变化后重试。

### 修正与轮询过滤规则

- **修正窗口**：只取 `(lowSeq, highSeq]`。低水位之前的写入已包含在某个一致快照
  之中，高水位之后的写入交给轮询，二者不重不漏。
- **轮询** `Poll()`：对每个**已完成**范围，原子读取
  `ReadLog(highSeq, start, end)`，只应用处理位置之后（`seq > highSeq`）
  且键落在该范围闭区间内的日志，按序号顺序应用后把处理位置推进到本次读到的
  源表最后序号。序号只进不退，每条日志恰好生效一次 → 输出不重复、不回退。
- 即使新日志全部落在范围外，处理位置也会推进（范围固定，区间外日志永不再相关）。
- 多个范围互不相交，各范围的行集合构成视图分区；`Poll` 对所有范围整批计算、
  统一做行数上限校验，任一范围超限则**整批拒绝**，视图与全部处理位置不变。

### 拒绝原因（可 `errors.Is` 区分）

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidRange` | `start > end` |
| `ErrRangeOverlap` | 新范围与已有范围闭区间相交 |
| `ErrPhaseOrder` | 未 Begin 先快照/完成、重复快照、完成后再推进、end 不匹配等 |
| `ErrViewLimitExceeded` | 完成或轮询应用后视图行数超过 `WithMaxViewRows` 上限 |

被拒绝的操作不改变视图、处理位置或水位。

### 日志

每个操作通过 `slog` 记录**输入**（`input`）、**输出**（`output`）与
**判定依据**（`basis`），拒绝时另带 `reason`。可用 `WithLogger` 注入自定义
logger（如 JSON handler）。

### 使用示例

```go
src := snapshot.NewSource()
sy := snapshot.NewSyncer(src, snapshot.WithMaxViewRows(1_000_000))

src.Put(1, "a")
sy.BeginRange(1, 100)       // 低水位
sy.SnapshotRange(1, 100)    // 一致快照
sy.CompleteRange(1, 100)    // 高水位 + 日志修正 + 安装视图

// 源表持续写入……
for {
    if n, err := sy.Poll(); err == nil && n == 0 {
        time.Sleep(time.Second) // 已追平
    }
    _ = sy.View() // 按键升序的视图拷贝
}
```

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

# 高重复次数验证并发稳定性 / 确定性
go test -race -count=20 ./snapshot/
go test -run TestDeterminism -count=10 ./snapshot/

# 单个包 / 单个用例
go test ./snapshot
go test -run TestConcurrentWriters ./snapshot

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

`snapshot` 包测试覆盖：键恰好落在范围边界（含端点与单键范围）、快照三步之间
持续写入并验证修正后视图等于源表、各类非法输入（非法范围、相交、阶段错乱、
行数超限）及其无副作用、同输入序列输出完全一致、多写者并发下停写后最终追平、
以及日志中输入/输出/判定依据字段。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
