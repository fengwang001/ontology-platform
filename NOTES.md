# NOTES — 事件时间去重窗口（ttl=10, delay=2）
# 记 a5 = ID:a 首见TS:5；wm=迄今最大TS-2；过期条件 wm >= 首见+10
步 事件     wm  ②清除         判定 步后记忆             dups
1  (a,5)    3   无             新   a5                   0
2  (b,8)    6   无             新   a5,b8                0
3  (a,9)    7   无             重   a5,b8                1
4  (c,17)   15  清a(15=5+10)   新   b8,c17               1
5  (a,14)   15  无             新   a14,b8,c17           1
6  (b,20)   18  清b(18=8+10)   新   a14,c17,b20          1
7  (d,4)    18  无；④即清d4    新   a14,c17,b20          1
8  (d,6)    18  无；④即清d6    新   a14,c17,b20          1
9  (c,26)   24  清a(24=14+10)  重   c17,b20              2
10 (a,25)   24  无             新   c17,b20,a25          2

甲：>= 时第4步清 a，第5步(a,14)为新。若改严格 >：第5、6步变重复（步6的 18=8+10 边界也不清 b），dups 错成 4（正确 2）。
乙：重复时刷新首见TS：第3步仍判重复但 a5 被刷成 a9；第5步变重复（15<9+10 未清）并刷成 a14；第9步 c 重复刷成 c26；dups=3（正2）；第10步后记忆为 a25,b20,c26。
丙：永不清 / 按到达10条计数过期（本序列10条内计数法无人到期）：第5,6,8,10步错判重复，dups=6、新事件仅4条（少4条，正8）。
d 两步皆新：wm=18>=4+10，d4 在④立即清除；d6 同理（18>=16），故第8步查无 d；两条都输出下游。

不变量（代码保证位置 / 钉住测试）：
1 朴素参照一致：dedup/dedup.go Process 严守 ①更新wm ②清除 ③查重 ④插入后即清；api_test.go TestNaive、api.SelfCheck
2 记忆精确：dedup.go purge 按过期点最小堆逐项弹出、插入后即判即清；api_test.go TestMemExact
3 水位线单调：dwin/dwin.go Observe 只取历史最大 TS；api_test.go TestWatermarkMonotonic
4 失败不留痕：api.go Feed 先 Snapshot、任何拒绝路径 rollback 整体回滚；api_test.go TestRejectedNoTrace
（扫描计数为 dedup 非导出字段 scan，无公开出口，仅同包 dedup_test.go TestScanSublinear 白盒读取）
