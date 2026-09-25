# TTL 状态存储：推导与不变量

## 七步推导（ttl=10，过期判定 `now−Ts ≥ TTL`，Get 不刷新 Ts）

| 步 | 操作 | now | k1.Ts | k2.Ts | k3.Ts | 本步结果 | 之后保留 |
|---|---|---|---|---|---|---|---|
| 1 | Set(k1,v1,5) | 5 | 5 | – | – | 写入 k1 | k1 |
| 2 | Set(k2,v2,7) | 7 | 5 | 7 | – | 写入 k2 | k1,k2 |
| 3 | Get(k1,14) | 14 | 5 | 7 | – | 14−5=9<10 → 返回 (v1,true)，Ts 不变 | k1,k2 |
| 4 | Set(k3,v3,15) | 15 | 5 | 7 | 15 | 写入 k3 | k1,k2,k3 |

注：「保留」指物理未删除；按逻辑判定第 4 步后 k1 已过期（15−5=10≥10），故 `View(15)` 只列 k2,k3。
| 5 | Get(k1,15) | 15 | 5 | 7 | 15 | 15−5=10≥10 → 过期，删 k1，返回 ok=false | k2,k3 |
| 6 | Sweep(20) | 20 | – | 7 | 15 | k2:20−7=13≥10 删；k3:20−15=5<10 留 → 删 1 个 | k3 |
| 7 | Get(k3,25) | 25 | – | – | 15 | 25−15=10≥10 → 过期，删 k3，返回 ok=false | （空） |

最终视图为空。

- **(甲)** 若判定写成 `now−Ts > TTL`：第 5 步 15−5=10 不满足 >10 → 返回 (v1,true)，k1 **不被删除**（正确应为 ok=false 且删除）。
- **(乙)** 若 Get 刷新 Ts：第 3 步把 k1.Ts 改成 14；第 5 步 15−14=1<10 → 返回 (v1,true) 且再刷新为 15；第 6 步 Sweep(20) 时 20−15=5<10 → k1 仍保留。最终视图多出 `k1=v1`（正确实现里 k1 已消失），与正确实现不等。
- **(丙)** 若 Sweep 是空操作：第 6 步谁也没删（正确应删 k2）；k2 此后再未被 Get，惰性删除永远碰不到它 → k2 永久残留，违反不变量 2（Sweep 后保留的每个 key 必须满足 `now−Ts < TTL`，内存有界）。

## 四条不变量：保证位置与钉住它的测试

1. **与朴素判定一致**：`ttl.Store.View` 先 sweep 再全量拷贝，过期判定唯一出口是 `entry.Expired`；测试 `api_test.TestNaiveConsistency`（随机操作序列对拍朴素模型）。
2. **有界内存**：`ttl.Store.sweepLocked` 按 Ts 最小堆弹出所有过期项、`getLocked` 命中过期立即 `delete`；测试 `api_test.TestBoundedMemory`、`ttl_test.TestSweepDeletesExpired`。
3. **时钟单调**：`ttl.Store.checkClock` 在任何状态修改前执行，`now < maxNow` 直接返回 `ErrBackwardClock` 且不更新 `maxNow`；测试 `ttl_test.TestClockMonotonic`。
4. **失败不留痕**：`New` 先验 `ttl>0`、`Set` 先验 key 与时钟再写、被拒操作在写路径之前返回；测试 `api_test.TestFaultInjection`（三类哨兵错误互不相同、拒后状态不变）。
