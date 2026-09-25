# NOTES

## 三、W=10 九事件推导（列：步 事件 判定 | a b c | Seen）
1  B(1,a)  应用        | 1 0 0 | 1
2  B(3,b)  应用        | 1 1 0 | 2
3  O(12,b) 应用        | 1 2 0 | 3
4  B(5,a)  应用        | 2 2 0 | 4
5  O(5,a)  去重 no-op  | 2 2 0 | 4
6  B(7,c)  应用        | 2 2 1 | 5
7  O(11,c) 应用        | 2 2 2 | 6
8  B(9,a)  应用        | 3 2 2 | 7
9  O(3,b)  去重 no-op  | 3 2 2 | 7
甲：CompleteBackfill 后 Backfill(2,a) 报 ErrBackfillCompleted，View 不变（a=3）；漏「已完成」闸门则 a 错成 4。
   边界误写成 Seq>W 时，Backfill(10,d) 被误收，d 错成 1（正确：整批拒，d 不存在）。
乙：不做去重，a 计到 4 次（1/5/5/9）、b 计到 3 次（3/12/3），即 a=4、b=3（正确 a=3、b=2）。
丙：回填段单独累加为 a3 b1 c1，覆盖后 b=1、c=1（正确各 2），在线增量 O12/O11 被吞。
   分段再相加/覆盖会双计跨段重复（Seq 5、3）或吞掉在线段；只有「全序按 Seq 去重后计数」与交错顺序无关。

## 二、四条不变量：代码保证位置 / 钉住它的测试
1 朴素参照一致：backfill.go apply 仅在 dedup.Set.Add 首次插入成功时 counts[key]++ —— TestNaiveReference
2 切换一致/顺序无关：Backfill 与 Online 共用同一 apply；Complete 只置 completed，不碰 counts —— TestOrderIndependence
3 去重幂等：dedup.go 开放寻址哈希表，同一 Seq 二次 Add 返回 false，View/Seen 不变 —— TestIdempotent
4 失败不留痕：Backfill/Online 先全批 validate 再在锁内 apply，任一非法整批零修改 —— TestRejectedBatchAtomic
