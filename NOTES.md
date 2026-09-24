# 逻辑复制槽两个 LSN：推导与不变量

## 8 步分步表（槽创建于 LSN 5：confirmed=5, restart=5）

| 步 | confirmed | restart | 需保留事务(Begin) | 本步发出 | 被拒及原因 | WAL 回收到 |
|---|---|---|---|---|---|---|
| E1 Append 10–30 | 5 | 5 | T1(10), T2(20) | T1@30 [] | 否 | 无（<5） |
| E2 Confirm(30) | 30 | 20 | T2(20) | 无 | 否 | <20（回收 10） |
| E3 Append 40–50 | 30 | 20 | T2(20), T3(40) | T3@50 [] | 否 | <20 |
| E4 Confirm(45) | 30 | 20 | T2(20), T3(40) | 无 | 拒：45 非已发出事务的提交 LSN | <20 |
| E5 Append 60–80 | 30 | 20 | T2(20), T3(40) | T2@80 [x,y] | 否 | <20 |
| E6 Confirm(50) | 50 | 20 | T2(20) | 无 | 否 | <20 |
| E7 Restart() | 50 | 20 | T2(20) | T2@80 [x,y] | 否 | <20 |
| E8 Confirm(80) | 80 | 80 | 无 | 无 | 否 | <80（回收 20–75） |

（甲）E2 时 T2 仍进行中、Begin@20，其 WAL 记录必须保留供崩溃重读，故 restart=min(30,20)=20≠30。若 restart 跟随 confirmed：E2 后 restart=30、E6 后 restart=50；LSN<30 的记录（20 Begin T2、25 Change x）在 E2 即被回收，E7 从 LSN 30 重读，重发的 T2 缺 Change x（只剩 [y]）。
（乙）只算进行中事务：E2 后 restart=20（与正确值相同），E6 后 restart=50（正确值 20——T2 已提交@80>50 需保留，Begin@20），从 E6 起与正确值不同，20/25 被回收、T2 的 Begin 与 Change x 丢失。E4 被拒因 45 不是任何已发出事务的提交 LSN（当时仅发出 T1@30、T3@50）。
（丙）E7 重发 T2@80 [x,y]；T2 在 E5 已发出过 → 至少一次带来重复。T1@30≤50、T3@50≤50 故不重发。若崩溃改在 E5 之后、E6 之前：confirmed=30、restart=20，从 LSN 20 重读，重发 T3@50 [] 与 T2@80 [x,y]。

## 四条不变量：保证位置与钉住测试

1. 与朴素参照一致：`slot.recompute` 按 Begin LSN 有序弹出失效条目、队首即最小 Begin；`api.SelfCheck` 内 naive 模型逐操作比对。测试 `TestNaiveRef`。
2. 可恢复：`wal.Reclaim` 只回收 LSN<restart，restart 恒 ≤ 需保留事务的 Begin；`slot.Restart` 从首条未回收记录重读、只发 commit>confirmed 的事务。测试 `TestRestartRecover`。
3. 单调有序：confirmed 只在 `slot.Confirm` 接受时升为某事务提交 LSN；restart=min(confirmed,…)≤confirmed 且回收点单调不降。测试 `TestMonotonic`。
4. 失败不留痕：`wal.Append` 先整体校验再应用；`slot.Append` 先干跑 maxOpen 再解码；`slot.Confirm` 三类拒绝均先于任何状态修改返回。测试 `TestRejectNoTrace`。
