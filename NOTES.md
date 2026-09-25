# NOTES

八步表（maxBytes=100，条目字节=len(Key)+8=9；v0 空快照 0 字节）

| 步 | 操作后 | V | used | 保留版本(最老) | 本步产出 |
|---|---|---|---|---|---|
| 1 | Bcast{a1,b2} | 1 | 18 | {0,1}(0) | 无 |
| 2 | Bcast{c3,d4} | 2 | 54 | {0,1,2}(0) | 无 |
| 3 | Fact a@1 | 2 | 54 | {0,1,2}(0) | (a,1,1) |
| 4 | Bcast{a9} | 3 | 90 | {0,1,2,3}(0) | 无 |
| 5 | Fact a@1 | 3 | 90 | {0..3}(0) | (a,1,1) |
| 6 | Fact a@3 | 3 | 90 | {0..3}(0) | (a,9,3) |
| 7 | Bcast{e5,f6} | 4 | 90 | {3,4}(3) | 无（144，删v0仍144→删v1=126→删v2=90） |
| 8 | Fact a@1 | 4 | 90 | {3,4}(3) | stale 丢弃（1<3） |

(甲) 第7步后保留 {3,4}、used=90、最老可查=3。若「超限即整批拒绝」：第7步被拒（V=3,used=90,{0..3}），第8步 a@1 会错查到 v1 输出 **(a,1,1)**（应 stale 丢弃、不产行）。
(乙) 第5步正确 **(a,1,1)**；若永远查当前表，a 已在第4步被改成 9，错成 **(a,9,1)**，破坏**不变量1（与批量重算一致）**。
(丙) 按「快照字节最大者」淘汰：第7步先删 v4(54)，used=90 即止，e5 随 v4 丢失，`Fact e@4` 错成 **miss**（正确应 **(e,5,4)**）。无下限实现：最后两版之和已 >cap 时（如 cap=10、首批 18 字节）会把 V-1 乃至 V 也删光——当前版本不可查，且下次 Broadcast 失去 upsert 基表。

不变量（代码保证位置 / 钉住测试）：

1. 与批量重算一致：`join/join.go` Join 经 `dim.Lookup` 按事实 Vsn 取该版快照；`api/api.go` SelfCheck 用独立参考模型重放比对。**TestInvariantRecompute**
2. 版本单调、被拒不变：`dim/dim.go` Broadcast 先在副本上算好新快照与淘汰结果，最后才一次性赋值，失败路径不触任何字段。**TestVersionMonotonic**
3. 上限/旧→新淘汰/保底 V,V-1：`dim/dim.go` Broadcast 淘汰循环（used>cap 且 len>2 才删最旧，仍超则拒）。**TestEvictionOrderAndCap**
4. 失败不留痕：`dim/dim.go`、`join/join.go` 哨兵错误前置校验；api 测试比对错误前后 V/used/快照/行/丢弃/miss 全状态。**TestRejectionLeavesNoTrace**

附：map 定位靠 `dim/dim.go` 非导出 `probes`(atomic) → **TestProbeCountConstant**（dim 白盒测试）；并发 → **TestConcurrentJoin**。
