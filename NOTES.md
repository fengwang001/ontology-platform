# NOTES

推导（p=0, retention=10；驱逐条件 ts < now-10）：
|步|操作|C|cp|归档|Restart|
|1|Commit(100,0)|100|∅|[(100,0)]|100|
|2|Checkpoint|100|100|[(100,0)]|100|
|3|Commit(110,10)|110|100|[(100,0),(110,10)]|110|
|4|Commit(120,15)|120|100|[(100,0),(110,10),(120,15)]|120|
|5|Evict(20)|120|100|[(110,10),(120,15)]|120|
|6|Commit(130,30)|130|100|[(110,10),(120,15),(130,30)]|130|
|7|Evict(40)|130|100|[(130,30)]|130|
|8|Evict(45)|130|100|[]|100|
(甲) 步7正确=130；若只读cp忽略归档→错成100。
(乙) 步8正确=100（cp兜底）；若只读归档忽略cp→空归档无位点=-inf（代码以 math.MinInt64 表示）。
(丙) 严格<：10<10为假，(110,10)保留、恢复110；若误写成<=则(110,10)被清，退回cp=100。

不变量（代码位置 / 钉住的测试）：
1 批量重算一致：off.Recover 取 max(cp, 幸存归档最大off)，api.Restart 逐分区照搬（off/off.go、api/api.go）/ TestRestartMatchesBatchRecomputation（随机序列独立重算）+ SelfCheck 八步常量。
2 检查点永不驱逐：cp 独立于归档列表存放，arc.Evict 只从头部切归档、不碰 cp（arc/arc.go、off/off.go）/ TestSelfCheck（第8步归档清空仍恢复100）。
3 恢复单调：最新未检查点条目未被驱逐时幸存最大位点=C且C只增，max(cp,C)不减（off.Recover）/ TestSelfCheck（mono 标记逐步断言不减，第8步允许回退）。
4 失败不留痕：全部校验先于任何赋值，非法即原样返回哨兵错误（api/api.go New；off/off.go Commit/Checkpoint/Evict）/ TestRejectedOperationsLeaveNoTrace。
复杂度：arc 非导出字段 checked 只记一次 Evict 的检查数，遇首个保留项即停 / arc TestEvictInspectBound；并发只读 / api TestConcurrentReaders。
