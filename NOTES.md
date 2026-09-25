# 批量事件时间对齐器 NOTES

## 三、单 key "k" 八事件分步表（对齐=本批已接受事件运行最小 TS；值=已接受事件全局 LWW）

| 行 | 批 | TS | V | 判定 | 本步后本批对齐 | k 最终值 |
|---|---|---|---|---|---|---|
| 1 | 0 | 10 | 1 | 接受（第0批无下限） | 10 | 1 |
| 2 | 0 | 5 | 2 | 接受 | 5 | 1 |
| 3 | 1 | 20 | 3 | 接受（20>=A0=5） | 20 | 3 |
| 4 | 1 | 12 | 4 | 接受（12>=5） | 12 | 3 |
| 5 | 2 | 8 | 5 | 迟到丢弃（8<A1=12） | 无（本批尚无接受） | 3 |
| 6 | 2 | 12 | 6 | 接受（12>=12，等号不迟到） | 12 | 3 |
| 7 | 3 | 25 | 7 | 接受（25>=A2=12） | 25 | 7 |
| 8 | 3 | 25 | 9 | 接受（TS 平局，后到胜） | 25 | 9 |

终态：A=[5,12,12,25]，Dropped=1，V=9。

- (甲) 对齐错用批内**最大** TS：A0 错成 10，**A1 错成 20（正确 12）**；第 2 批 (TS=12,V=6) 被**误判迟到**（(8,5) 本就迟到，不算误判），丢弃数从 **1 错成 2**。
- (乙) LWW 错成**最小 TS 胜**：最终错成 **V=2**（TS=5 那条），正确 **V=9**。
- (丙) 平局错成**先到者胜**：第 3 批 TS=25 平局留下 **V=7**，正确后到 **V=9**。

## 二、四条不变量：保证位置 / 钉住测试

1. 与批量重算一致：`lww.go` Register.Feed 用 `ts >= c.ts` 覆盖（平局后到胜），在线应用等价于批内按 TS 升序；钉住：`TestInvariantBatchRecompute`（随机批/随机到达序对拍参考重算）。
2. 对齐单调且正确：`aln.go` Accept 拒收 `ts < prev` 保单调、运行最小 curMin 保最小、close 空批继承 prev；钉住：`TestInvariantAlignedTimesMonotonic`。
3. 丢弃与迟到一致：仅 Accept=false 时 `lww.go` Register.Feed 内 `dropped++`，丢弃路径不碰 curMin/vals；钉住：`TestInvariantDroppedSemantics`。
4. 失败不留痕：`api.go` New 与 V.Feed 四类校验均在任何状态修改前 return 哨兵错误；钉住：`TestInvariantRejectionLeavesNoTrace`。

复杂度：aln.Aligner 非导出字段 cmps，每接受一条只与 curMin 比较一次；钉住：`aln_test.go` 的 `TestRunningMinComparisons`（m=100..10000 断言 cmps==m，非 m(m-1)/2）。
