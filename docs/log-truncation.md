# 日志截断的位点一致维护器（logtrunc）

`logtrunc` 包周期回收已持久化的日志前缀，保证**截断位点之前必已落盘**，
使崩溃恢复能收敛到一致状态且始终可复现。

## 核心概念

- **连续偏移序号**：条目从实际起始偏移 `start` 开始逐一递增，任何时刻可见
  区间都是 `[start, next)` 的连贯序列。
- **持久化位点 `persisted`**：表示 `[0, persisted)` 已全部落盘。位点
  **只进不退**，由 `DeclarePersisted(upTo)` 显式声明推进；回退
  （`ErrPersistRegression`）或越过已追加最大偏移（`ErrPersistOutOfRange`）
  都整体拒绝，且不改变日志、位点与标记。
- **截断标记 `marker`**：最近一次截断第一步落盘的目标起始偏移，持久化在
  `truncate.marker` 文件中。
- **实际起始 `actual`**：数据文件 `log.data` 头部记录的起始偏移。

## 截断两步顺序

`Truncate(to)` 要求 `to <= persisted`（截断位点之前必已落盘），否则以
`ErrTruncateBeyondPersisted` 整体拒绝；`to` 越出 `[start, next]` 以
`ErrTruncateOutOfRange` 整体拒绝。校验全部先于任何状态变更，一次失败不得
改变日志、持久化位点与标记。合法截断分两步：

1. **先落截断标记**：将 `to` 原子写入 `truncate.marker`（临时文件 + fsync +
   rename + 目录 fsync）。
2. **再物理删除**：原子重写 `log.data`，丢弃 `[start, to)` 前缀。

两步都在同一把写锁内完成，因此读者永远看不到“标记已推进但前缀仍部分可见”
的中间态；`Read`/`Verify` 持有读锁，可与追加、截断安全并发。

## 崩溃双判定规则

崩溃可能发生在两步之间，留下标记与实际起始不一致。`Open` 恢复时比较
`marker` 与 `actual`：

| 关系 | 判定 | 处理 |
| --- | --- | --- |
| `actual == marker` | 干净 | 直接使用，无需收敛 |
| `actual < marker` | 崩溃于两步之间 | 补删 `[actual, marker)`，收敛到 `start = marker` |
| `actual > marker` | 越删（损坏） | 以 `ErrOverDeletion` 报告损坏并整体拒绝打开 |

恢复后不变量由 `Verify()` 自检：偏移自 `start` 连续、`persisted` 不越界、
`marker == actual`。

## 周期回收

`StartRecycler(ctx, interval, retainEntries)` 每隔 `interval` 调用一次
`RecycleOnce`，把已持久化前缀截断回收（保留最近 `retainEntries` 条），
回收位点同样受“不得超过持久化位点”约束。

## 本地验证：朴素追加再删除核对

用朴素模型交叉核对实现（见 `TestNaiveCrossCheck`）：

1. 朴素模型只维护一个切片：所有 `Append` 的 payload 依次追加；
2. 每次成功 `Truncate(to)`，就在朴素模型上删除 `[naiveStart, to)` 前缀；
3. 每轮用 `Read(0, Next())` 读出实现的全部可见条目，与朴素模型逐条比对
   偏移与内容；
4. 最后重新 `Open`（模拟崩溃重启）再比对一次，确认结果可复现。

运行：

```bash
go test -race -v ./logtrunc
```

单测日志会打印每个操作及其后的持久化位点、截断标记、实际起始与判定依据。
