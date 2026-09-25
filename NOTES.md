# NOTES — 滑动去重窗口

## 八步推导（W=5, maxOpen=3；窗口半开 [last, last+W)）

| # | 事件 | 判定 | 该ID last | 保留集合 ID→last | 淘汰 |
|---|---|---|---|---|---|
| 1 | (X,10) | 接受(新) | 10 | {X:10} | 无 |
| 2 | (X,15) | 接受(d=5>=W) | 15 | {X:15} | 无 |
| 3 | (Y,20) | 接受(新) | 20 | {X:15,Y:20} | 无 |
| 4 | (Z,30) | 接受(新) | 30 | {X:15,Y:20,Z:30} | 无 |
| 5 | (Y,24) | 重复(d=4<W) | 20(不变) | {X:15,Y:20,Z:30} | 无 |
| 6 | (W,40) | 接受(新,表满) | 40 | {Y:20,Z:30,W:40} | 淘汰 X(last=15 最小) |
| 7 | (Y,25) | 接受(d=5>=W) | 25 | {Y:25,Z:30,W:40} | 无 |
| 8 | (Z,32) | 重复(d=2<W) | 30(不变) | {Y:25,Z:30,W:40} | 无 |

- (甲) d==W 属 `d>=W` → **接受**，last_X=15。若错写成 `d<=W` 算窗内，第2步会错判**重复**，X 的 last 错留 **10**。
- (乙) 第6步正确淘汰 **X**(last=15 最小)。若错淘汰 last 最大者，会错淘汰 **Z**(last=30)；第8步 (Z,32) 因 Z 已被淘汰按新事件错判**接受**（正确为重复）。
- (丙) 第5步重复不刷新，Y 的 last 保持 **20**。若重复也刷新到 24，第7步 (Y,25) d=1<W 会错判**重复**（正确为接受）。

## 四条不变量：保证位置与钉住测试

1. 窗口不重叠：接受仅当 `ts-last>=W`（win.Duplicate 半开判定，`win/win.go`）；测试 `TestWindowBoundary`（dedup/dedup_test.go）。
2. 与朴素参照一致：流式判定只依赖每 ID 的 last（dedup.Table.Dedup，`dedup/dedup.go`）；测试 `TestNaiveReference` 逐事件比对朴素重放。
3. 淘汰正确：仅新 ID 且表满时淘汰 last 最小者，重复/乱序不改 last 不触发淘汰（`dedup/dedup.go` Dedup 的 evict 分支）；测试 `TestEviction`。
4. 失败不留痕：参数校验先于任何写（api.New / Dedup 入口，`api/api.go`）；测试 `TestFailureNoTrace`。

复杂度：map 定位，checked 计数器为 1，不随 m 增长；测试 `TestLookupIsConstant`（dedup/dedup_test.go，包内读非导出字段）。
并发：api.Deduper 内一把 sync.Mutex；测试 `TestConcurrentDedup`。
