# 物化视图双缓冲重建（double-buffered rebuild）

`ontology.View` 是一个按 `key` 聚合增量计数的物化视图，支持在**不停止前台服务**的前提下，
于后台依据全量事件日志重建视图并**原子切换**。

## 数据模型

- `Event{Key string, Delta int64}`：一条增量事件；`Key == ""` 或 `Delta == 0` 均为非法事件。
- 前台视图 `front map[string]int64`：唯一对外读取的视图。
- 全量日志 `log []Event`：按到达顺序追加，是朴素重放的唯一事实来源。
- 重建状态：`snapshotSeq`（快照点）、`back`（重建缓冲区）、`pending`（待补齐列表）。

## 正常工作流

### 1. 服务与写入

- `Apply(events)`：先校验整批，再在同一把写锁内把每条事件累加到前台并追加日志。
- `Snapshot()`：在读锁内整体拷贝前台 map 返回，因此读端永远拿到一份一致的完整视图，
  且可与写入并发进行。

### 2. 开始重建

- `BeginRebuild()` 记录快照点 `snapshotSeq = len(log)`，并把重建缓冲区 `back` 置为空 map。
- 重建期间前台照常服务；重建中重复 `BeginRebuild` 被拒绝（`ErrRebuildAlreadyActive`）。

### 3. 后台重放

- `Replay(events)` 将快照点之前的历史事件**按日志顺序逐条**累加进 `back`（朴素重放）。
  便捷取数：`LogEvents()[:SnapshotPoint()]`。
- 非重建中调用 `Replay` 被拒绝（`ErrReplayWithoutRebuild`）。

### 4. 双写与切换前补齐

- 重建期间到达的 `Apply`：事件照常立即进前台与日志，**同时**追加到 `pending`。
- `SwitchRebuild()` 在同一个写锁临界区内依次完成：
  1. 把 `pending` 中的事件按顺序补齐到 `back`；
  2. 令 `front = back`（指针切换）；
  3. 清空全部重建状态（`back/pending/snapshotSeq`、`rebuilding=false`）。

  因为补齐与换指针在同一临界区完成，读端只会看到两种状态之一：
  **完整的旧前台**，或**包含全部历史与重建期间增量的完整新前台**，不存在半成品视图。
- 非重建中切换被拒绝（`ErrSwitchWithoutRebuild`）。

### 5. 中止

- `AbortRebuild()` 丢弃 `back` 与 `pending`、清空重建状态；前台与日志完全不变。
- 非重建中中止被拒绝（`ErrAbortWithoutRebuild`）。中止后可以重新 `BeginRebuild`。

## 一致性依据

- 重建结果 = 对 `log[:snapshotSeq]` 朴素重放 + 顺序补齐 `pending`；
  而 `pending` 恰为快照点之后写入的全部事件，因此切换后的视图等于对**全量日志朴素重放**的结果。
- 计数视图满足交换/结合性，最终值只取决于日志中各键增量之和。

## 错误模型（互不相同，可用 `errors.Is` 判定）

| 错误 | 触发条件 |
| --- | --- |
| `ErrSwitchWithoutRebuild` | 非重建中调用切换 |
| `ErrAbortWithoutRebuild` | 非重建中调用中止 |
| `ErrRebuildAlreadyActive` | 重建中再次开始重建 |
| `ErrReplayWithoutRebuild` | 非重建中调用重放 |
| `ErrEmptyKey` | 事件键为空（`Apply`/`Replay`） |
| `ErrZeroDelta` | 事件增量为零（`Apply`/`Replay`） |

- 非法事件以 `*BatchError` 包装（含批内下标 `Index`），同时支持 `errors.Is` 与 `errors.As`。
- **批原子性**：任一批内任一条非法，整批不生效——前台、日志、待补齐列表、重建缓冲区均不变
  （事件在加锁前完成全批校验，锁内无失败路径）。

## 并发策略

- 单把 `sync.RWMutex`：写操作（`Apply`/`BeginRebuild`/`Replay`/`SwitchRebuild`/`AbortRebuild`）
  取写锁，`Snapshot` 取读锁并拷贝 map。
- 切换本质是一次写锁内的指针替换，开销与 map 大小无关。

## 本地验证

```bash
# 全量测试（详细日志含输入、结果与判定依据）
go test -v ./ontology

# 竞态检测 + 反复执行
go test -race -count=5 ./...

# 格式与静态检查
gofmt -l .
go vet ./...
```

测试覆盖：重建重放与朴素重放一致、重建期间双写、切换前补齐、中止后前台不变可重建、
重建期间读始终命中前台、四类非法控制操作与两类非法事件（含整批回滚、状态不变）、
8 写者并发写入与并发读的最终计数与快照自洽性、重建与并发写入同时进行后切换结果仍等于全量朴素重放。
