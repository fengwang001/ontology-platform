# Hinted handoff：八步推导与不变量

设定：R0/R2 在线、R1 起始宕机，maxHints=3，key=k；未写记 -@0，hints 记 R1 缓冲。
1. W(a,5)：R0=a@5；R1=(宕机)-@0 hints=[a5]；R2=a@5。在线写入 + hint×1
2. W(b,7)：R0=b@7；R1=(宕机)-@0 hints=[a5,b7]；R2=b@7。在线写入 + hint×1
3. W(c,6)：R0/R2=b@7（6<7 跳）；R1 hints=[a5,b7,c6]（=3，未超限）。在线跳过、hint 照追加
4. W(d,8)：R1 将变 4>3 → 整体拒绝；R0/R2 仍 b@7、hints 仍 3 条。失败不留痕
5. Up(1)：a5 应、b7 应、c6 跳 → R1=b@7；applied=2 skipped=1，缓冲清空
6. Down(1)；W(e,9)：R0/R2=e@9；R1=(宕机)b@7 hints=[e9]；R2=e@9。写入 + hint×1
7. W(f,9)：R0/R2 并列跳过仍 e@9；R1 hints=[e9,f9]（宕机无条件追加）
8. Up(1)：e9 应、f9 并列跳 → applied=1 skipped=1；全副本=e@9，缓冲清空
(甲) 第 4 步若先写在线副本、不回滚：R0/R2 错成 d@8，R1 仍滞留旧 hints。
(乙) 重放若无条件按序最后条胜：R1 错成 c@6，版本由 7 降到 6。
(丙) 写与重放判据都用 >=：第 7 步在线 9>=9 改 f@9，第 8 步 f9>=9 再应用，全副本错成 f@9。

不变量（代码保证位置 / 钉住的测试函数）：
1 朴素一致：hh.go 在线 ver>cur 与 Up 的 Replay 回调同一严格判据；TestEightStep、TestConcurrentWrites。
2 重放幂等：hint.go Replay 遍历后清空缓冲；TestReplayIdempotent，api.SelfCheck 内置复检。
3 版本不回退：两处严格 >，且 hh.go Write 先全量预飞行再改任何状态；TestEightStep、TestRejectedOpsNoTrace。
4 失败不留痕：hint.go Append 先判满后追加；hh.go 非法参数先返回、超限先预飞行；TestRejectedOpsNoTrace。
扫描计数器为非导出字段 hint.Buffer.scans，追加路径恒为 0：TestAppendScanIsO1（m=100/1000/10000）。
