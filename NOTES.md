# NOTES

T=4 七步（操作 | delta | base | 自动合并）:
1. Set a=1 | [a:1] | {} | 否
2. Set b=2 | [a:1,b:2] | {} | 否
3. Set c=3 | [a:1,b:2,c:3] | {} | 否
4. Del c   | [] | {a:1,b:2} | 是（c:删折叠后移除）
5. Set b=9 | [b:9] | {a:1,b:2} | 否
6. Set c=7 | [b:9,c:7] | {a:1,b:2} | 否
7. Set c=8 | [b:9,c:7,c:8] | {a:1,b:2} | 否

甲：合并后 base={a:1,b:2}，Read(c)=("",false)。若错折叠成 base["c"]=""（保留空值键），会错读为 ("",true)——空值被当成存在。
乙：Read(c)=("8",true)，合并代价 3；若「旧→新扫描、命中第一条即停」，会错得 "7"。
丙：代价 3 = |delta| ≤ T−1，与历史操作总数无关：base 是 O(1) 查表，Read 只扫当前 delta。去掉 compact，则 |delta|=7、代价 7，随写入总数 m 线性增长，违反不变量 3 与第四节「代价 ≤ T+常数、不随 m 线性增长」。

不变量（保证位置 | 钉住测试）:
- I1 朴素重算一致：store.Read 从 base 起全量顺序扫 delta 逐条 merge.Apply（store/store.go Read）| TestNaiveRecompute
- I2 墓碑语义：merge.Apply 遇 Del 返回 Result{OK:false}，compact 用 delete(base,key) 移除而非置空（merge/merge.go、store.go compactLocked）| TestTombstone
- I3 代价有界：每次 Set/Del 后 len(delta)≥T 立即整体折叠进 base 并清空（store.go compactLocked）| TestMergeCostBounded
- I4 失败不留痕：New/Set/Del 在触碰任何状态前返回哨兵错误（store/store.go、api/api.go）| TestRejectedOpsLeaveNoTrace
- 并发一致：RWMutex 保护 base/delta，lastCost 为 atomic.Int64（store/store.go）| TestConcurrentReadersWriters
