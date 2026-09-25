# EBR 推导与不变量

初始 G=0，T1=1、T2=2，节点 A=10。

| 步 | G | 活跃(id→ep) | 退休(id→ep) | 返回 |
|---|---|---|---|---|
| 1 Enter(1) | 0 | 1→0 | — | nil |
| 2 Enter(2) | 0 | 1→0,2→0 | — | nil |
| 3 Retire(10) | 0 | 1→0,2→0 | 10→0 | nil |
| 4 Exit(1) | 0 | 2→0 | 10→0 | nil |
| 5 AdvanceEpoch | 1 | 2→0 | 10→0 | — |
| 6 Reclaim | 1 | 2→0 | 10→0 | [] |
| 7 Exit(2) | 1 | — | 10→0 | nil |
| 8 Reclaim | 1 | — | — | [10] |

(甲) 退休即释放=UAF：T2 仍活跃于 ep0、可能正读 A，free 后访问悬空内存。正确：A 入退休列表标 ep0，等宽限期。
(乙) 不释放：0 < minActive(0) 不成立。误判成 e<G 则 0<1 成立而释放；错在 G 推进不证明读者已离开——T2 入区后未重入，公告仍是 ep0，故过早释放/UAF。
(丙) 不能。T2 永留 ep0 ⇒ 最小活跃 epoch≡0，0<0 恒假，A 永不释放=永久泄漏（不死线程使该节点的宽限期永不到来）。

## 不变量（保证位置 / 钉住测试）

1. 与朴素参照一致：`api.SelfCheck` 内置随机序列，逐次比对互斥锁+整表扫描的朴素回收器返回集合；`TestNaiveEquivalence` 钉。
2. 安全性：`retire/retire.go` 的 Reclaim 仅释放 `e < 最小活跃ep`（无活跃视为 +∞）；`TestEightSteps`(第6步不释放) 与 `TestConcurrent`(原子断言) 钉。
3. 最终回收：全部 Exit 后 Reclaim 以 +∞ 判定清空退休表，不泄漏；`TestEventualReclaim` 钉。
4. 失败不留痕：`epoch.Enter/Exit`、`retire.Retire` 全部先校验后改表，四哨兵互异；`TestRejectedOpsNoTrace`、`TestSentinelsDistinct` 钉。
