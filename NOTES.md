# NOTES

八行表（现存快照 | 本步过期 | 本步删除 | 文件存储）：
1. S1{ts10,f1,f2} | 无 | 无 | {f1,f2}
2. S1{f1,f2} S2{ts20,f2,f3} | 无 | 无 | {f1,f2,f3}
3. S1 S2 S3{ts30,f3,f4} | 无 | 无 | {f1,f2,f3,f4}
4. S1 S2 S3 S4{ts40,f4,f5} | 无 | 无 | {f1,f2,f3,f4,f5}
5. S2{f2,f3} S3{f3,f4} S4{f4,f5} | S1 | f1 | {f2,f3,f4,f5}
6. S2 S3 S4 S5{ts50,f5,f6} | 无 | 无 | {f2,f3,f4,f5,f6}
7. S4{f4,f5} S5{f5,f6} | S2,S3 | f2,f3 | {f4,f5,f6}
8. S5{f5,f6} | S4 | f4 | {f5,f6}

(甲) 第5步：S1 三条都不满足；S2、S3 仅满足条件2（ts>15）；S4 三条全满足。
若条件1∩2再并条件3：交集={S4}，只保留 S4；过期 S1,S2,S3；删 f1,f2,f3（f4 被 S4 引用留下）。
(乙) 只减当前快照 S4 的文件{f4,f5}：会删 f1,f2；保留快照 S2 因缺 f2 不可读；违反不变量1（删除集≠朴素结果）、2（保留快照可读）、3（存储≠现存并集）。
(丙) 第8步 S5：50>50 不成立、N=0，仅靠条件3（当前快照永不过期）保留。无此条则 S4,S5 全部过期，删 f4,f5,f6，之后剩 0 个快照。

不变量保证位置 / 钉住测试：
1. 朴素一致：保留判定 snapchain.Partition（三条件取并），删除集 fileref.Store.Expire（只减归零引用）→ TestNaiveReferenceRandom
2. 保留可读：fileref.Store.Expire 仅在 refs 计数归零时删除，引用>0 必留 → TestRetainedReadable
3. 无泄漏：api.Expire 后 Files() 恰为现存快照文件并集（提交即写、归零即删）→ TestNoLeakAfterExpire
4. 失败不留痕：api.Commit/Expire 先 Chain.CheckCommit 与 Store.CheckCommit 全通过再变更 → TestRejectedOpsLeaveNoTrace
