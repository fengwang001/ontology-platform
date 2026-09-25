# NOTES：小表广播 map-side join（maxBytes=100，单字符 Key，每条目 9B；初始 V=0、v0 空快照 0B）

| # | 操作 | V | used | 保留版本(最老可查) | 本步产出 |
|---|---|---|---|---|---|
| 1 | Broadcast a1 b2 | 1 | 18 | {0,1} old=0 | 无 |
| 2 | Broadcast c3 d4 | 2 | 54 | {0,1,2} old=0 | 无 |
| 3 | Fact{a,Vsn=1} | 2 | 54 | {0,1,2} old=0 | 行 (a,1,1) |
| 4 | Broadcast {a,9} | 3 | 90 | {0,1,2,3} old=0 | 无 |
| 5 | Fact{a,Vsn=1} | 3 | 90 | {0..3} old=0 | 行 (a,1,1) |
| 6 | Fact{a,Vsn=3} | 3 | 90 | {0..3} old=0 | 行 (a,9,3) |
| 7 | Broadcast e5 f6 | 4 | 144→90 | 新快照54B；按旧到新淘汰 v0(0B),v1(18B),v2(36B)，留 {3,4} old=3 | 无 |
| 8 | Fact{a,Vsn=1} | 4 | 90 | {3,4} old=3 | 1<3 stale 丢弃，dropped++，无行 |

(甲) 第7步后 keep={3,4}、used=90、最老可查=3。若超限即整批拒绝而不淘汰：V 仍=3、v1 仍保留，第8步会错输出行 (a,1,1)（正确应 stale 丢弃）。
(乙) 第5步正确为 (a,1,1)；若 join 永查当前表，第5步错成 (a,9,1)，破坏不变量1（必须按事实 Vsn 对应版本快照查）。
(丙) 按「快照字节最大者」淘汰会先删 v4（54B，即当前版本自身），Fact{e,Vsn=4} 遂错成 miss（正确为行 (e,5,4)）。无下限实现会在「淘汰到仅剩 V 而 used 仍超限」（如新快照自身 > maxBytes）时连 V/V-1 也淘汰，使当前版本不可查；正确行为是整批拒绝、V 回退、状态复原。

不变量保证位置与钉住测试：
1. 与批量重算一致：dim.Store.Lookup 按 vsn 定位快照（dim/dim.go），api.Engine.Join 逐条产出 Row（api/api.go）—— TestJoinMatchesBatchRecompute
2. 版本单调/拒绝回滚：Store.Broadcast 先在局部切片模拟淘汰，超限才提交，否则不触碰 s.v/s.hist/s.used —— TestBroadcastRejectRollsBack
3. 上限/淘汰次序/保留最后两版：Store.Broadcast 的 for used>max && len>2 淘汰循环（恒从队首）—— TestEvictionOrderAndFloor
4. 失败不留痕：四类哨兵错误（dim.ErrEmptyKey/ErrEmptyBatch/ErrMaxBytes/ErrFuture）均在任何状态提交前返回；Engine 计数只在校验后变更 —— TestRejectedOpsLeaveNoTrace
另：map 定位探针（dim 非导出字段 probe）由 TestProbeCountBound 钉住；并发由 TestConcurrentJoin；自检由 TestSelfCheck。
