# Marzullo 区间算法 NOTES

## 一、推导（f=1，need=K−f=2；A=[8,12] B=[10,12] C=[19,21]）
扫描线（位置升序，同位 L 先于 R）：
| 位置 | 端点 | 端点后 count | 候选共识 |
| 8  | L(A) | 1 | 否（<2） |
| 10 | L(B) | 2 | 进入：lo=10 |
| 12 | R(A) | 1 | 离开：hi=12，得 [10,12] |
| 12 | R(B) | 0 | 否 |
| 19 | L(C) | 1 | 否 |
| 21 | R(C) | 0 | 否 |
最短候选即 [10,12]。
(甲) 12 属于共识（闭），CountAt(12)=2；右开 [10,12) 丢掉 t=12——A、B 在该点仍一致，共同右端点上的共识被抹掉。
(乙) f=0 需三台全一致，交集 [8,12]∩[10,12]∩[19,21]=∅，应报 ErrNoConsensus；f=1 容许 C 坏，得 [10,12]。
(丙) error=−1 → C=[21,19]（lo>hi 倒置），Add 即拒（ErrNegativeError）、不留痕；倒置使 L(21) 排在 R(19) 之后，破坏 L≤R 前提，扫描计数被污染，故不得参与。

## 二、不变量保证位置与钉测
1. 与朴素重算一致：csync.go `(*Set).Consensus` 经 buildEvents+marz.Sweep 每次全量重扫 → TestConsensusMatchesNaive
2. 边界闭合：marz.go `sortEvents` 同位 L 先 R 后、闭端点计入候选 → TestBoundaryClosed
3. CountAt 正确：csync.go `(*Set).CountAt` 用 #(Lo≤t) − #(Hi<t) 两次二分 → TestCountAtMatchesNaive
4. 失败不留痕：api.go `Add/New/Consensus` 全部校验通过后才改状态 → TestRejectedOpsLeaveNoTrace
复杂度：csync.go 非导出字段 probes，二分每比较一次 +1 → TestCountAtProbeCount；并发 → TestConcurrentConsistency；自检 → TestSelfCheck。
