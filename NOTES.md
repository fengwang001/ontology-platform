# NOTES — W=10, b=5，窗口 (T-10, T]，T=当前最新 TS

| 步 | 事件(Key,TS) | 桶 idx=TS/5 | last 更新 | 本步 Distinct |
|---|---|---|---|---|
| 1 | (1,1) | 0 | last[1]=1 | 1 |
| 2 | (2,3) | 0 | last[2]=3 | 2 |
| 3 | (5,4) | 0 | last[5]=4 | 3 |
| 4 | (1,6) | 1 | last[1]=6（从桶0移入桶1） | 3 |
| 5 | (3,11) | 2 | last[3]=11；T=11,c=1，2@3 仍 >1 | 4 |
| 6 | (4,13) | 2 | last[4]=13；T=13,c=3，逐条淘汰 2@3（3≤3） | 4 |

（甲）左开 `last>T-W`：key2 的 last=3 恰在左边界，**不纳入**，最终 Distinct=**4**。误写成闭区间 `last>=T-W` → key2 被误纳，错成 **5**。
（乙）桶0=[0,5) 内含 key2@3、key5@4；正确条件 (idx+1)*b≤c 即 5≤3 不成立，桶0保留。误按桶起点 idx*b≤c（0≤3）整桶删 → 误删仍有效的 **key5**（key2 本就过期），只剩 1,3,4，错成 **3**。
（丙）右闭：key4 的 TS=13=T 纳入，Distinct=4。误写成右开 `TS<T` → **key4** 被排除，错成 **3**。

## 不变量：保证位置 / 钉住的测试函数

1. 与朴素重算一致：`wdist/wdist.go::expire` 先用算术整桶边界批量删、再按有序队列逐条精确剔除边界桶，共同维护 `live`；钉于 `api/api_test.go::TestNaiveConsistent`。
2. last 单调不减：`wdist/wdist.go::Apply` 仅在 `Check` 已确认全局 TS 非递减后写入，同 Key 重访先离旧桶再写新值，`last[Key]` 永不减小；钉于 `wdist/wdist_test.go::TestLastMonotonic`。
3. 边界语义精确：`win/win.go::InWindow` 严格 `ts>T-W && ts<=T`、`MaxExpiredBucket` 用 `(T-W)/b-1` 只指认整桶 ≤ 左界的桶；钉于 `api/api_test.go::TestBoundarySemantics`。
4. 失败不留痕：`api/api.go::New` 返回哨兵错误；`Feed` 先整批校验（含跨批 TS 不回退）通过后才改状态；钉于 `api/api_test.go::TestRejectLeavesNoTrace`。
并发只读：`api/api.go` 的 `sync.RWMutex`；钉于 `api/api_test.go::TestConcurrentDistinct`。
过期检查计数：`wdist/wdist.go` 非导出字段 `checked`（每次触发只做一次边界比较）；钉于 `wdist/wdist_test.go::TestExpireCheckBounded`。
