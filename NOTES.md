# NOTES：滑动窗口 MAX 双栈（W=4；栈按 底→顶 写 `值/聚合`，窗口按 早→晚）

规则要点：`Push` 先压 `in`，超 W 立即 `Evict`；故第 5 步翻转时 `in` 有 5 个元素。

| 步 | 操作 | in（底→顶） | out（底→顶） | 翻转 | 逐出 | Max | 搬移 |
|---|---|---|---|---|---|---|---|
| 1 | Push3 | 3/3 | — | 否 | 无 | 3 | 0 |
| 2 | Push1 | 3/3 1/3 | — | 否 | 无 | 3 | 0 |
| 3 | Push3 | 3/3 1/3 3/3 | — | 否 | 无 | 3 | 0 |
| 4 | Push2 | 3/3 1/3 3/3 2/3 | — | 否 | 无 | 3 | 0 |
| 5 | Push1 | — | 1/1 2/2 3/3 1/3 | 是(搬5) | 3 | 3 | 5 |
| 6 | Push0 | 0/0 | 1/1 2/2 3/3 | 否 | 1 | 3 | 5 |
| 7 | Evict | 0/0 | 1/1 2/2 | 否 | 3 | 2 | 5 |
| 8 | Push2 | 0/0 2/2 | 1/1 2/2 | 否 | 无 | 2 | 5 |
| 9 | Evict | 0/0 2/2 | 1/1 | 否 | 2 | 2 | 5 |
| 10 | Evict | 0/0 2/2 | — | 否 | 1 | 2 | 5 |
| 11 | Evict | — | 2/2 | 是(搬2) | 0 | 2 | 7 |

逐出序列 3,1,3,2,1,0 = 压入序列前缀，余 [2]。
**甲**（单调队列，`<=` 弹尾、队首值相等则弹）：第 5 步后队 [2,1]，Max 给 **2**（真实窗口 [1,3,2,1]、Max=**3**，并列 3 被早弹，错）；第 9 步后队**已空**（第 8 步新 2 入队时 `<=` 弹掉旧 2，第 9 步队首 2 又随逐出被弹），Max 无队首（实现常给 0/报错），真实窗口 [1,0,2]、Max=2。改严格 `<`：第 9 步后队剩 **[2]**，Max=2 正确。
**乙**（每次逐出都翻转）：第 6 步错逐 **0**（应逐 1；翻转后栈顶是最新元素），Max=3（应 3，碰巧相同）；第 7 步错逐 **1**（应逐 3），Max 给 **3**（应 **2**）。
**丙**（数组、逐出值==旧 Max 才重扫）：重扫在第 5 步扫 4、第 7 步扫 3、第 9 步扫 3，合计 **10**；第 9 步逐出的是并列最大值（旧 2，余 [1,0,2] 仍含 2），重扫后 Max 不变（仍 2）。双栈累计搬移 **7 = Push 次数 7**（第 5 步搬 5 + 第 11 步搬 2），每个元素至多被搬一次。

## 不变量落点（保证位置 / 钉住的测试）

1. 与朴素参照一致：`twin.Values`/`twin.Max` 与「仅 out 空才翻转」（twin.go）；TestElevenSteps、TestStackAggregates（twin_test.go），TestNaiveConsistency（api_test.go）。
2. 先进先出：`twin.Evict`（twin.go），翻转=整栈逆序后弹栈顶；TestFIFO、TestElevenSteps（twin_test.go）。
3. 聚合正确：入栈即算在 `stk.Push`（stk.go），核验在 `twin.AggregatesValid`；TestStackAggregates（twin_test.go）。
4. 失败不留痕：api 全部先校验（NaN/W）后加锁变更，`PushAll` 先整批预检（api.go）；TestRejectedAtomicity（api_test.go）。
均摊 O(1)：非导出 `twin.moves` 只增于翻转，TestAmortizedMoves 断言 moves≤Push 数且 Max 前后计数不变。并发：api 的 `sync.RWMutex`，TestConcurrentSnapshots。
