# NOTES

## 一、推导 W=10 B=3，单键 K，到达序 5,12,20 / 7,22,25 / 8,31,15 / Flush:9
1. 批1[5,12,20] wm=20；+：(K,[0,10),1)、(K,[10,20),1)；迟到：无（20∈[20,30)，其 end=30>wm 不关）
2. 批2[7,22,25] wm=25；+：无（end=30>25）；迟到：-(K,[0,10),1)、+(K,[0,10),2)
3. 批3[8,31,15] wm=31；+：(K,[20,30),3)（31∈[30,40)，end40>31）；迟到：-(K,[0,10),2)/+(…,3)、-(K,[10,20),1)/+(…,2)
4. Flush[9] wm=+∞；+：(K,[30,40),1)（[0,10)已关不重发）；迟到：-(K,[0,10),3)、+(K,[0,10),4)
（甲）[10,20) 正确=1（仅12）；误当右闭则 20 错入该窗→错成 2，[20,30) 少 1；[20,30) 正确=3（20,22,25）。
（乙）old=2（批2 的 7 已把 [0,10) 抬到 2）；恒用关窗值 1：TS=8 错成 -1/+2（应 -2/+3），TS=9 错成 -1/+2（应 -3/+4）；每条 - 撤的都不是当前值，+ 反复把值置回 2，下游最终错成 2（正确 4）。
（丙）每次迟到都重发完整 +、不写 -：关窗 1 条 + 三次迟到各 1 条，共 4 条 +；下游重复累加 1+2+3+4=10（正确 4）。

## 二、不变量落点（代码位置 / 钉住的测试）
- I1 View=按 floor(TS/W) 的批量重算：win.Index 左闭右开归属（win/win.go）+ mbatch 计数与 Flush 全关窗（mbatch/mbatch.go trigger/flush）；TestViewMatchesBatch
- I2 每前缀至多一值、- 恰撤当前值、末尾=批量：trigger 第1步入堆/计数前先快照 old 再 +1，第3步发 -old/+old+1（mbatch/mbatch.go）；TestChangelogPrefixes
- I3 每窗 + 恰一次、迟到只发 delta：mbatch.closed 集合，关窗仅从 open 堆弹出时发（mbatch/mbatch.go）；TestPlusOnce
- I4 拒绝不留痕（W、B 非正、空 Key）：mbatch.New 先拒 W/B、mbatch.Feed 先整批扫空 Key 再触碰任何状态，api 仅加锁转发（api/api.go）；TestRejectedNoTrace、TestSentinelDistinct
