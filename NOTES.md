# NOTES：快照过期与孤儿文件判定

## 八步推导（每步之后的状态；文件集升序）

| 步 | 现存快照（号{文件集}） | 本步过期 | 本步删除 | 文件存储 |
|---|---|---|---|---|
| 1 Commit(10,+f1f2) | S1{f1,f2} | 无 | 无 | {f1,f2} |
| 2 Commit(20,+f3,-f1) | S1{f1,f2} S2{f2,f3} | 无 | 无 | {f1,f2,f3} |
| 3 Commit(30,+f4,-f2) | S1 S2 S3{f3,f4} | 无 | 无 | {f1,f2,f3,f4} |
| 4 Commit(40,+f5,-f3) | S1 S2 S3 S4{f4,f5} | 无 | 无 | {f1..f5} |
| 5 Expire(N=1,T=15) | S2{f2,f3} S3{f3,f4} S4{f4,f5} | S1 | f1 | {f2,f3,f4,f5} |
| 6 Commit(50,+f6,-f4) | S2 S3 S4 S5{f5,f6} | 无 | 无 | {f2..f6} |
| 7 Expire(N=2,T=35) | S4{f4,f5} S5{f5,f6} | S2,S3 | f2,f3 | {f4,f5,f6} |
| 8 Expire(N=0,T=50) | S5{f5,f6} | S4 | f4 | {f5,f6} |

**(甲)** 第5步：S2 仅满足条件2（20>15）；S3 仅条件2（30>15）；S4 三条全满足（最新1个、40>15、当前快照）。若条件1与条件2取交集再并条件3：仅 S4 被保留，S1/S2/S3 过期，删除 {f1,f2,f3}（过期文件并 {f1,f2,f3,f4} 减保留文件 {f4,f5}）。
**(乙)** 只减当前快照文件时，第5步删除 {f1,f2}（S1 的 {f1,f2} 减 S4 的 {f4,f5}）。f2 被误删，保留快照 S2 缺 f2 不可读，违反不变量 2（保留快照可读）与不变量 3（存储不再等于现存快照文件并集）。
**(丙)** 第8步 S5 的 ts=50 不大于 T=50、N=0，仅靠条件3「当前快照永不过期」保留。若无此条：S4、S5 全部过期，删除 {f4,f5,f6}，剩 0 个快照。

## 四条不变量的落点

1. 与朴素参照一致：`snapchain.Chain.Expire` 三条取并划分保留/过期，`fileref.Store.ApplyExpire` 按引用计数删归零文件；测试 `TestRandomMatchesNaive`（api/api_test.go，随机序列对照朴素参照），自检 `API.SelfCheck`（八步对照上表）。
2. 保留快照可读：`fileref.Store.Register` 提交即写入并加引用，`ApplyExpire` 只删计数归零者；测试 `TestRandomMatchesNaive`（每步校验存储==现存快照文件并集）、`TestConcurrentReads`。
3. 无泄漏、当前快照在：`Chain.Expire` 条件3保当前快照，`ApplyExpire` 归零即物理删除；测试 `TestRandomMatchesNaive`、`TestConcurrentReads`，自检 `API.SelfCheck`。
4. 失败不留痕：`api.Commit`/`api.Expire` 全部校验先于任何状态变更；测试 `TestFaultInjection`（另有大 m 复杂度测试 `TestExpireVisitsIndependentOfM`，fileref/fileref_test.go）。
