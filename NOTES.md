# NOTES — partition affinity + drain/migrate

## 八步推导（初始 P0/P1/P2 均 Active 且为空）
| 步 | 操作 | 结果 | P0 集合 | P1 集合 | P2 集合(态) | C0/C1/C2 |
|---|---|---|---|---|---|---|
| 1 | Assign(a,P0) | 成功 | {a} | {} | {}(A) | 1,0,0 |
| 2 | Assign(b,P0) | 成功 | {a,b} | {} | {}(A) | 2,0,0 |
| 3 | Assign(c,P1) | 成功 | {a,b} | {c} | {}(A) | 2,1,0 |
| 4 | Assign(d,P2) | 成功 | {a,b} | {c} | {d}(A) | 2,1,1 |
| 5 | BeginDrain(P2) | 成功 | {a,b} | {c} | {d}(Draining) | 2,1,1 |
| 6 | Assign(e,P2) | 被拒：P2 Draining，亲和冲突 | {a,b} | {c} | {d}(D) | 2,1,1 |
| 7 | Put(d,"x") | 被拒：owner Draining，不写入 | {a,b} | {c} | {d}(D) | 2,1,1 |
| 8 | Migrate(P2,P0) | 成功 | {a,b,d} | {c} | {}(Removed) | 3,1,0 |

甲：漏清 from 计数 → Count(P2) 错成 1（正确 0）；全表总数错成 4（正确 3）。
乙：第 7 步静默写入后，x 随 d 在第 8 步被整体带到 P0，最终 Get(d) 错成 ("x",true)；正确为 ("",false)。脏写会被迁移带走。
丙：e 在排空期被误纳，随迁归属 P0，Count(P0) 错成 4（正确 3）。故排空期必须拒绝一切亲和变更；不变量 1 只统计**被接受**的 Assign——e 被拒即永不进入归属/迁移集合。

## 四条不变量：代码位置 / 钉住的测试
1. 与批量重算一致：router.(*Router).SelfCheck 用 affinity 全表重算 owner→计数并与 part.Len、acceptedAssigns 比对；TestSelfCheckInvariants、TestEightStepTable。
2. 迁移一致性：router.Migrate 持写锁把 from.members 与 affinity 整体搬到 to，再置 from=Removed（搬后 from.Len()==0）；TestEightStepTable、TestSelfCheckInvariants。
3. 排空与冲突：router.Assign/Put 仅在 part 状态 Active 放行，router.Get 对 Draining 仍可读；TestRejectedOpsLeaveNoTrace、TestEightStepTable。
4. 失败不留痕：router 全部写操作先做完校验再改任何状态（校验失败直接返回哨兵错误）；TestRejectedOpsLeaveNoTrace。
并发原子性由 TestConcurrentReadAtomicMigrate 钉住；扫描分区数常量由 router.lastMigrateScanned 记录、TestMigrateScanCountConstant 钉住。
