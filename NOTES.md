# NOTES

## 七步推导（ttl=10，过期判定 now−Ts ≥ 10）

| 步 | 操作 | now | k1.Ts | k2.Ts | k3.Ts | 本步结果 | 之后保留 |
|---|---|---|---|---|---|---|---|
| 1 | Set(k1,v1,5) | 5 | 5 | – | – | 写入 k1 | k1 |
| 2 | Set(k2,v2,7) | 7 | 5 | 7 | – | 写入 k2 | k1,k2 |
| 3 | Get(k1,14) | 14 | 5 | 7 | – | 14−5=9<10 → 返回 v1, ok=true，不删不刷新 | k1,k2 |
| 4 | Set(k3,v3,15) | 15 | 5 | 7 | 15 | 写入 k3 | k1,k2,k3 |
| 5 | Get(k1,15) | 15 | – | 7 | 15 | 15−5=10≥10 → 过期，删 k1，ok=false | k2,k3 |
| 6 | Sweep(20) | 20 | – | – | 15 | k2:20−7=13≥10 删；k3:20−15=5<10 留；返回 1 | k3 |
| 7 | Get(k3,25) | 25 | – | – | – | 25−15=10≥10 → 过期，删 k3，ok=false | （空） |

- (甲) 若判定写成 `now−Ts > TTL`：第 5 步 15−5=10 不 >10，错误返回 v1、ok=true，k1 当场不删（要到第 6 步 Sweep(20) 才被删）。
- (乙) 若 Get 刷新 Ts：第 3 步把 k1.Ts 改成 14；第 5 步 15−14=1<10 返回 v1 并把 Ts 再刷成 15，第 6 步 20−15=5<10 仍保留。最终视图={k1:v1}，而正确实现最终视图为空——k1 错误残留。
- (丙) 若 Sweep 是空操作：第 6 步谁都不删（正确应删 k2）。k2 此后从未再被 Get，永远残留，违反不变量 2（有界内存：Sweep 后不得残留过期 key）。

## 四条不变量的保证位置与钉住测试

1. 与朴素判定一致：`ttl/ttl.go` 的 `View` 先 `sweepLocked` 再全量列出；测试 `TestNaiveConsistency`（api/api_test.go，随机序列对拍朴素 map）。
2. 有界内存：`ttl/ttl.go` 的 `sweepLocked` 弹堆至队首未过期、`Get` 过期即删；测试 `TestBoundedMemory`（api/api_test.go）、`TestSweepCheckedBounded`（ttl/ttl_test.go）。
3. 时钟单调：`ttl/ttl.go` 的 `checkClock` 先校验再更新 `maxNow`，拒绝时不写；测试 `TestClockMonotone`。
4. 失败不留痕：所有公开方法先完成全部校验（`ttl/ttl.go` 的 `Set/Get/Sweep/View` 入口）再动状态；测试 `TestRejectLeavesNoTrace`。
