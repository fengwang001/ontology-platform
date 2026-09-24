# NOTES — 推导（size=10 delay=3 lateness=5 early=2；wm=maxTS−3，只进不退，初始 −∞）

|步|wm|窗口|判定|本步变更日志|该窗计数
1 TS=2：wm=−1，[0,10)，正常计入，无，c=1
2 TS=5：wm=2，[0,10)，正常计入，+(K,[0,10),2)（early 快照），c=2
3 TS=9：wm=6，[0,10)，正常计入，无（3 非 2 倍数），c=3
4 TS=15：wm=12，事件入 [10,20) 正常；wm 越过 10，w0 on-time：+(K,[0,10),3)；w1 c=1（无输出）
5 TS=4：wm=12，[0,10)，迟到接受（12<10+5），−(K,[0,10),3) +(K,[0,10),4)，c=4
6 TS=12：wm=12，[10,20)，正常计入 c=2 触 early：+(K,[10,20),2)，c=2
7 TS=7：wm=12，[0,10)，迟到接受，−(K,[0,10),4) +(K,[0,10),5)，c=5
8 TS=20：wm=17，事件入 [20,30) 正常 c=1；wm≥10+5，w0 被 purge（清除无日志）
Flush：+(K,[10,20),2) +(K,[20,30),1)；视图 {[0,10):5,[10,20):2,[20,30):1}，丢弃 0

甲：early 若清零 → 第 4 步 on-time 错成 1（正确 3）；第 5、7 步迟到累加后最终 w0 错成 3（正确 5）。
乙：不记 fired → 第 5 步重复 on-time，多输出 +(K,[0,10),3)（若计数后触发则 +4）：直接违反不变量 3（on-time 至多一次），且缺 −3/+4 撤回配对，不变量 2 的前缀自洽随之失效。
丙：误写成 wm≥end 即 purge → 第 4 步后 w0 状态被删，第 5、7 步迟到事件无窗可归被丢弃；最终 w0 错成 3（正确 5），丢弃数错成 2（正确 0）。

不变量 → 代码保证位置 / 钉住测试：
1 批量一致：Flush 按 end 堆排空所有未触发窗（wagg.go Flush/advance），View 物化完整日志（heap.go View）→ TestBatchRecompute
2 前缀自洽：迟到先 −旧值 紧接 +新值，+ 为 upsert（wagg.go apply）→ TestChangelogPrefixes
3 on-time 一次 + wm 单调：winState.fired 去重（wagg.go advance）、observe 只取 max（wagg.go observe）→ TestOnTimeOnce
4 失败不留痕：Feed 先整批预校验（空 Key/参数）并预算新增窗口数，通过后才落任何状态 → TestRejectedBatchAtomic
复杂度：container/heap 按 end 排序定位，inspected 只数窥视/弹出（wagg.go advance、heap.go endHeap）→ TestInspectionSublinear
