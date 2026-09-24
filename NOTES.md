# NOTES

## 八步推导（ttl=10；age = now − last；过期 ⇔ age ≥ 10）

| 步 | 操作 | k1.last | k2.last | k3.last | 本步结果 |
|---|---|---|---|---|---|
| 1 | P(k1,A,100) | 100 | – | – | 写入 |
| 2 | P(k2,B,110) | 100 | 110 | – | 写入 |
| 3 | G(k1,109) | 100 | 110 | – | 命中 A（age=9<10） |
| 4 | G(k1,110) | 已删 | 110 | – | 无匹配（age=10≥10，惰性清除 k1） |
| 5 | P(k3,C,105) | – | 110 | 105 | 写入 |
| 6 | C(115) | – | 110 | 已删 | 清 1 个：k3(age=10)；k2(age=5) 保留 |
| 7 | G(k2,120) | – | 已删 | – | 无匹配（age=10，惰性清除 k2） |
| 8 | P(k1,D,200) | 200 | – | – | 写入 |

- (甲) `G(k1,110)` 应返回**无匹配**（age=10=ttl 即过期）；`now−last>ttl` 的实现此刻错返回「命中 A」。
- (乙) `C(115)` 清 k3（age=115−105=10）、留 k2（age=5）。事件水位实现取 max eventTime=110 判 `110−105=5<10`，把 k3 **错判成未过期而保留**。印证清理时钟（墙钟 now）与事件时钟（eventTime）必须分离。
- (丙) 迟到 `P(k1,E,50)`：last=max(200,50)=**200 不回退**。覆盖实现 last=50，则 now≥60 即误判过期——如 now=200 时 k1 本应活到 now<210 却被提前清除。last 单调才能抵御乱序/迟到事件。

## 四条不变量

1. 与朴素参照一致：`state.Get`/`state.Cleanup` 均只用 `ttl.Expired` 判定（state/state.go），堆顶惰性弹出；钉于 `TestMatchesNaive`（LCG 随机操作序列对照朴素 map 模型）。
2. 过期即无匹配：`state.Get` 命中过期条目即 delete 并返回无匹配；`state.Cleanup` 弹出全部过期堆顶；钉于 `TestExpiredIsNoMatch`。
3. last 单调：`state.Put` 仅在 `eventTime > 旧 last` 时推进（max 逻辑）；钉于 `TestLastMonotonic`（迟到事件后 Get 仍命中）。
4. 失败不留痕：空 Key → `state.ErrEmptyKey`、`ttl≤0` → `api.ErrInvalidTTL`，校验先于任何写；钉于 `TestRejectLeavesState`。
