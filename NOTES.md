# 变更流外键物化 — 推导与不变量

记哨兵：E1=子插父不存在(孤儿)、E2=删被引用父、E3=删不存在父、E4=删不存在子；视图内容按升序。

| # | 操作 | 判定 | 步后 ViewP | 步后 ViewC |
|---|---|---|---|---|
| 1 | C.ins(k1,p1) | 拒 E1 | {} | {} |
| 2 | P.ins(p1) | 成 | {p1} | {} |
| 3 | C.ins(k1,p1) | 成 | {p1} | {k1:p1} |
| 4 | C.ins(k2,p2) | 拒 E1 | {p1} | {k1:p1} |
| 5 | P.del(p1) | 拒 E2（k1 引用） | {p1} | {k1:p1} |
| 6 | C.ins(k3,p1) | 成 | {p1} | {k1:p1,k3:p1} |
| 7 | C.del(k1) | 成 | {p1} | {k3:p1} |
| 8 | C.del(k2) | 拒 E4 | {p1} | {k3:p1} |
| 9 | C.del(k3) | 成 | {p1} | {} |
| 10 | P.del(p1) | 成 | {} | {} |
| 11 | C.ins(k3,p1) | 拒 E1 | {} | {} |
| 12 | P.ins(p1) | 成 | {p1} | {} |
| 13 | C.ins(k3,p1) | 成 | {p1} | {k3:p1} |

(甲) 第5步拒 E2，p1 此刻被 k1 引用（k3 尚未插入）。级联误删 → ViewP={}、ViewC={}；静默允许悬垂 → ViewP={}、ViewC={k1:p1}，违反不变量2（永不悬空；也连带违反不变量1，朴素重放本应拒绝）。
(乙) 等待队列错实现：k2 指向的 p2 从未出现，队列却在第12步 p1 到达时把 k2 补挂上去 → ViewC 错成 {k2:p1,k3:p1}；正确视图 {k3:p1}。差异来自规则原文「没有任何等待队列：被拒的子行不记忆，父晚到后必须由上游重投子行」。
(丙) 第11步拒 E1：判定只看操作那一刻 p1 不存在（第10步已删），属「父晚到」（子第11步先到、父第12步才到），不留痕、靠第13步上游重投。第8步拒 E4：k2 第4步的插入已被拒、从未入库，故「不存在」。若记成「半存在」，第8步会被误判成删除成功（不返 E4），且第4～8步间 ViewC 一直凭空多出幻影行 k2。

不变量1 与朴素参照一致：fk 四个操作函数先判定、后改态（fk/fk.go）；钉住测试 TestThirteenSteps、TestRandomDifferential。
不变量2 永不悬空：fk.CIns 的父存在性前置检查与 fk.PDel 的引用前置检查（fk/fk.go）；钉住测试 TestRandomDifferential（每成功一步查悬空）。
不变量3 引用计数准确：sch.State.Refs 由 fk 在子行增删时同步维护（fk/fk.go），api.checkInvariants 逐步核对计数==ViewC 分组数；钉住测试 TestRandomDifferential、sch/sch_test.go 的 TestReferencedCheckIsConstant。
不变量4 失败不留痕：四个拒绝分支全部在任何写入之前 return（fk/fk.go）；钉住测试 TestRejectionLeavesNoTrace（四场景快照前后逐字段比对）。
