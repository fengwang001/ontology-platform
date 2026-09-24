# 373: checkpoint-consistent log truncation

八步（cp 列 "-" = 尚无 checkpoint；可读区间含两端；CR = 写标记后、物理删除前崩溃）：
```
step op            cp  tm  f   readable
 1   A(a)=0         -   0   0   [0,0]
 2   A(b)=1         -   0   0   [0,1]
 3   A(c)=2         -   0   0   [0,2]
 4   C(2)           2   0   0   [0,2]
 5   T(2)           2   2   2   [2,2]
 6   A(d)=3         2   2   2   [2,3]
 7   C(3)           3   2   2   [2,3]
 8   T(3)+CR        3   3   2   [2,3]
 R   Recover        3   3   3   [3,3]
```
(甲) 安全删除域由 K<=cp 限定为 [0,cp)：cp=2 时 T(3) 删 [0,3)，落在保护外的是 offset 2（条目 c）；先截断后 Checkpoint（在 C(3) 之前发 T(3)）或不校验 K<=cp 都会截掉它。
(乙) CR 时 tm=3、f=2，f<tm 判截断中断：补删 [2,3)（offset 2 的 c），收敛 f=tm=3。只看标记者以为已完成、不补删，c 残留热日志仍能被 Read 读到，状态永不收敛。
(丙) f>tm 表越删：先删后标时 f=3 已生效、tm=2 仍旧，[tm,f)=[2,3) 即 offset 2 的 c 已物理消失却无截断标记授权，序号留下永久空洞，只信标记的恢复把它当干净态，丢失不可察觉。先标后删时崩溃只会得 f<tm，可安全补删。

不变量（代码保证位置 / 钉住的测试）：
1. 截断前必已持久化：trunc.go 的 Truncate 只读单个字段 lg.CP()（metaReads=1）判 K<=cp，否则 ErrTruncateBeyondCheckpoint；TestTruncateRejectsUnpersisted、TestCheckpointReadCount。
2. 朴素参照一致：log.go 的 DropBefore/Read 与 trunc.go 的 f 同源，api.go 在同一把 RWMutex 下取快照；TestMatchesNaiveReference。
3. tm==f：trunc.go 的 Truncate 末尾才置 f=K（崩溃点在其前），Recover 的 ==/< /> 三分支收敛或报损坏；TestRecoveryTable、TestSelfCheck。
4. 失败不留痕：log.go 的 Checkpoint、trunc.go 的 Truncate/Recover 均先校验后赋值，哨兵错误；TestRejectedOpsLeaveNoTrace。
复杂度：metaReads 为非导出字段，合法 Truncate 时恒为 1，与 m 无关；TestCheckpointReadCount 白盒直读，demo 只取布尔结论。
并发：api.go 的 RWMutex 令“写标记+物理删除”整体原子，Read/Snapshot 只见连贯区间；TestConcurrentReadersCoherent（go test -race）。
