# 截断点位一致：推导与不变量

初始空日志：cp=无（记 -1），tm=0，f=0。

| 步 | 操作 | cp | tm | f | 可读 offset 区间 |
|---|---|---|---|---|---|
| 1 | A(a)=0 | -1 | 0 | 0 | [0,0] |
| 2 | A(b)=1 | -1 | 0 | 0 | [0,1] |
| 3 | A(c)=2 | -1 | 0 | 0 | [0,2] |
| 4 | C(2) | 2 | 0 | 0 | [0,2] |
| 5 | T(2) | 2 | 2 | 2 | [2,2] |
| 6 | A(d)=3 | 2 | 2 | 2 | [2,3] |
| 7 | C(3) | 3 | 2 | 2 | [2,3] |
| 8 | T(3) 后 CR（标记已落、物理删除前） | 3 | 3 | 2 | [2,3]（热日志残留，与标记矛盾） |

收敛后：补删 [2,3)，tm=f=3，可读 [3,3]。

**甲**：被错杀的是 **offset 3（d）**：cp=2 只担保 [0,2] 已持久化；先截断后 Checkpoint 或不校验 K≤cp 时 T(3) 删 [0,3)，把尚未持久化的 3 删掉且无法从副本重建。
**乙**：崩溃时 **tm=3、f=2**，f<tm 判「截断中断」，补删 [f,tm)=[2,3)，收敛到 f=3=tm。只看标记不看实际长度的实现会误判「已完成」，条目 2(c) 持续残留可见，标记与长度永不一致。
**丙**：f>tm 表示**越删**（数据已丢，必须报损坏）。错误实现先物理删除后写标记，删除后、写标记前崩溃：本例 f=3 而 tm 仍旧值 2，[tm,f)=[2,3)（即 offset 2 的 c）已从热日志消失，重启时无法证明它进过持久化副本；恢复若把 tm 抬到 f 等于默默追认丢失，故只能报 ErrRecoverOverDeleted。

## 不变量落点（代码位置 / 钉住的测试）

1. 截断前必已持久化：`trunc/trunc.go` Truncate 先经 readCP 读**单个** cp 字段、K>cp 即拒 / `TestTruncateBeyondCP`。
2. 与朴素参照一致：`api/api.go` SelfCheck 维护全量参照 offset→payload，每步比对 Read / `TestReferenceEquivalence`。
3. tm==f：Truncate 先写 marker 再 DeleteBefore，Recover 双判定（f<tm 补删、f>tm 报损）/ `TestEightStepCrash`。
4. 失败不留痕：三个互异哨兵错误，拒绝前快照、拒绝后 DeepEqual / `TestFailuresLeaveNoTrace`。
