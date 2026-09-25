# SSI 推导：记号 R/W=读/写集合，i/o=in/outConflict，sv=快照版本；初值 x10 y10 z5，全局版本 v=0
 1 T1.Begin        T1 sv0 R{} W{}
 2 T2.Begin        T2 sv0 R{} W{}
 3 T1.Read x       T1 R{x10}
 4 T2.Read x       T2 R{x10}
 5 T1.Read y       T1 R{x10,y10}
 6 T2.Read y       T2 R{x10,y10}
 7 T3.Begin        T3 sv0 R{} W{}
 8 T3.Write(z,99)  T3 W{z99}
 9 T3.Read z       读己之写=99，不记 R；T3 R{} W{z99}
10 T1.Write(x,-10) T1 R{x10,y10} W{x-10}
11 T1.Commit       无已提交 U，i0o0⇒提交 v1；状态 x=-10
12 T3.Commit       U=T1：R_T1∩W_T3=∅、W_T1∩R_T3=∅，i0o0⇒提交 v2；z=99
13 T2.Write(y,-10) T2 R{x10,y10} W{y-10}
14 T2.Commit       U=T1：R_T1∩W_T2={y}⇒i1；W_T1{x}∩R_T2≠∅且 v1>sv0⇒o1；i1∧o1⇒回滚；U=T3 无交集；终态 x=-10 y=10 z=99
甲（普通 SI，无 rw 检测）：T1 提交 x=-10、T2 提交 y=-10，终态 x=-10,y=-10，x+y=-20 违反 x+y≥0；SSI 正确终态 x=-10,y=10。
乙（漏读己之写）：T3 第 9 步读到快照旧值 z=5（应为 99）。
丙（T2 先提交）：T2 先 Commit 时无 U⇒提交 v1、y=-10；T1 后 Commit 对 U=T2 同样 i1∧o1⇒T1 被回滚；终态 x=10,y=-10。后提交者看到双边 rw 结构而被中止（first-committer-wins），两种序对应不同等价串行序。
故不变量 1 必须按提交（版本）序串行重放：原序重放 T1,T3 得 x=-10；换序重放 T2,T3 得 y=-10，与各自实际状态一致。
## 不变量保证位置 / 钉住测试
1 与串行参照一致：kv.(*Store).Apply 仅在未回滚时应用写集合并递增版本 — TestSerialReplay
2 写偏检测：kv.(*Store).Conflicts 算 in/out 双标志，ssi.(*Txn).Commit 据此回滚 — TestWriteSkew
3 读己之写：ssi.(*Txn).Read 先查写缓冲分支 — TestReadYourWrites
4 失败不留痕：ssi.(*Txn) 三哨兵错误且校验先于任何状态变更 — TestRejectedOpsNoTrace
复杂度：kv 的 key→readers/writers 索引 + ssi.Engine.lastChecked（非导出，存在性探测不逐条打开 Rec）— TestCommitCheckIndex
并发：kv.(*Store).Snapshot/Committed 持锁拷贝 — TestConcurrentSnapshots
