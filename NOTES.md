# NOTES — 按事件时间分日桶（America/New_York，DST 感知）

## 第三节推导：五个事件（均为 04:30 UTC）逐步表

| 事件 | EventTime | NY 本地时刻 | 偏移 | Bucket |
|------|-----------|-------------|------|--------|
| e1 | 1768451400 | 2026-01-14 23:30 EST | UTC-5 | 2026-01-14 |
| e2 | 1784089800 | 2026-07-15 00:30 EDT | UTC-4 | 2026-07-15 |
| e3 | 1772944200 | 2026-03-07 23:30 EST（春转未到） | UTC-5 | 2026-03-07 |
| e4 | 1793507400 | 2026-11-01 00:30 EDT（秋转未到） | UTC-4 | 2026-11-01 |
| e5 | 1773030600 | 2026-03-09 00:30 EDT（已春转） | UTC-4 | 2026-03-09 |

- (甲) e2 正确 = **2026-07-15**。固定 UTC-5 把本地时刻错算为 07-14 23:30，错成 **2026-07-14**（夏季真实偏移 -4，多减 1 小时退回前一天）。
- (乙) e1 正确 = **2026-01-14**。直接取 UTC 日期得 **2026-01-15**（UTC 已跨午夜，纽约仍是前一日 23:30）。
- (丙) e5 正确 = **2026-03-09**。固定 86400s 窗口（起点在春转前）：03-08 仅 23h，窗口边界比本地午夜晚 1h，下一窗 03-09 01:00 EDT 才开，e5（00:30 EDT）被留在上一窗，错成 **2026-03-08**。

## 四条不变量：代码位置与钉住它的测试

1. 与 stdlib 参照一致：`tzone.Zone.Date` 直接用 `time.Unix(ts,0).In(loc).Format("2006-01-02")`；测试 `api_test.TestMatchesReferenceAndMonotonic`（2026 全年逐小时对拍）。
2. 单调：同一实现保证日期字典序随 ts 不减；测试 `api_test.TestMatchesReferenceAndMonotonic`（同一循环内断言 prev<=cur）。
3. 边界一致：日界即本地午夜，由 IANA 时区数据决定（含 23h/25h 天）；测试 `api_test.TestMidnightBoundary`（全年每日 M 与 M-1）。
4. 失败不留痕：`bkt.Feed` 先整体校验再加锁累加，`tzone.Load`/`bkt.Bucket` 校验失败直接返回哨兵错误；测试 `bkt.TestFeedAtomic`、`api_test.TestDistinctErrorsAndNoTrace`。

另：O(1) 计数由 `bkt.Bucketer.counts` 预聚合 + 非导出 `lastScan` 保证，测试 `bkt.TestCountDoesNotRescan`；并发安全由 `sync.RWMutex` 保证，测试 `api_test.TestConcurrentReads`（`go test -race` 干净）。
