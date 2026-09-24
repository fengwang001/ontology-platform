# NOTES — 撤回日志按水位回收器（retraction-log GC）

## 第三节：八步分步表（活跃快照:水位 / G / 已回收 Seq 集合）

1. Append×4：活跃 ∅；G=0；回收 ∅
2. Open S1：S1=4；G=0；回收 ∅
3. Append(Seq5)：S1=4；G=0；回收 ∅
4. Open S2：S1=4, S2=5；G=0；回收 ∅
5. GC：S1=4, S2=5；G=4；回收 {1,2,3,4}
6. Append(Seq6)：S1=4, S2=5；G=4；回收 {1,2,3,4}
7. Close S1：S2=5；G=4；回收 {1,2,3,4}
8. GC：S2=5；G=5；回收 {1,2,3,4,5}

甲：正确实现第 5 步收 {1,2,3,4}（含等号：Seq4=G 也收）。若错写成 `Seq<G`：第 5 步只收 {1,2,3}，Seq4 漏收；第 8 步 Close S1 后 G=5，Seq4 才被补收（回收被错误推迟一轮）。
乙：若忽略快照、第 5 步按 `Seq<=当前Seq=5` 全收，则收 {1,2,3,4,5}；S1（W=4）Replay Seq4 得到「已回收」，而 4<=W 本应可读——违反不变量 1（快照不丢数据）。
丙：正确实现第 8 步新收 Seq5（累计 {1..5}）。若 Close 时不重估、缓存旧最小值 4，则第 8 步 G 停留 4、漏收 Seq5；违反不变量 2（有活跃快照时 G 必须等于最小活跃水位，此处应为 5）。

## 第二节：四条不变量的保证位置与钉住测试

1. 快照不丢数据：`gcer.go` 的 `GC()` 只在 `G<=最小活跃水位` 下删除 `seq<=G`，任何 `G<seq<=W` 的记录必仍可 Replay；钉于 `TestInvariantSnapshotData`。
2. G=最小活跃水位（无快照则 G=当前 Seq）：`gcer.go` 的 `gcTargetLocked()`/`minLocked()` 用小顶堆懒删求最小；钉于 `TestInvariantWatermarkMin`。
3. G 单调不减：`gcer.go` 的 `GC()` 中 `if ng > c.G { c.G = ng }` 夹取；钉于 `TestInvariantMonotonic`。
4. 失败不留痕：`gcer.go` 中 `Open/Close/Replay` 全部先做校验、命中哨兵错误即返回，此前不动 map/heap/计数器；钉于 `TestInvariantNoTraceOnFailure`。
附：堆取最小 O(1)（非导出计数器 `probeCount`）由 `gcer_test.go` 的 `TestMinProbeConstant` 钉住，m=100/1000/10000 均为小常数。
