# NOTES：重均衡迁移推导与不变量

## 九步推导（New(3)，a=10..e=50；owner(·,3): a0 b1 c2 d0 e2；owner(·,2): a0 b1 c0 d1 e1）
初始 P0={a:10,d:40} P1={b:20} P2={c:30,e:50}；M=[c,d,e]（c:2→0，d:0→1，e:2→1），a、b 归属不变不入 M。

| # | 操作 | P0 | P1 | P2 | cur | 备注 |
|---|---|---|---|---|---|---|
| 1 | Rescale(2) | a10 d40 | b20 | c30 e50 | 0 | 进迁移态 from=3 target=2 |
| 2 | Migrate(1) | a10 c30 d40 | b20 | e50 | 1 | 迁 c：P2→P0 |
| 3 | Get(c) | 不变 | 不变 | 不变 | 1 | c 已迁，双路由读 P0 → 30 |
| 4 | Put(d,41) | a10 c30 d41 | b20 | e50 | 1 | d 未迁，写旧归属 P0 |
| 5 | Migrate(1) | a10 c30 | b20 d41 | e50 | 2 | 迁 d：P0→P1，携带新值 41 |
| 6 | Restart() | a10 c30 | b20 d41 | e50 | 2 | 崩溃重启，保留检查点 cur=2 |
| 7 | Migrate(1) | a10 c30 | b20 d41 e50 | (空) | 3 | 迁 e：P2→P1 |
| 8 | Finish() | a10 c30 | b20 d41 e50 | — | 清零 | cur==len(M)，nparts=2 退出迁移态 |
| 9 | Get(d)/Get(e)/Get(c) | — | — | — | — | 41 / 50 / 30 |

(甲) Get(c)=30（c 已迁到 P0=owner(c,2)）。只按旧归属读：查 P2，c 已删 → 找不到。只按新归属读 Get(e)：查 P1 无 e → 找不到；正确应读 P2 得 50。
(乙) 若 Put 写新归属 P1：P1 得 d:41 而 P0 仍 d:40；第 5 步迁移把 P0 的 40 搬到 P1 覆盖 41 → Get(d)=40（错），正确 41。
(丙) 游标重置 0 → 第 7 步重迁 c；c 旧归属 P2 早已无 c；「读旧(零值)写新」把 c:0 写入 P0 覆盖 30 → Get(c)=0（正确 30）。违反不变量 2（恰好一次），并连带违反不变量 1。

## 四条不变量 → 代码位置 → 钉住它的测试
1. 与朴素参照一致/不丢不重：mig.Migrate 逐条「复制→删旧」、Finish 才改 nparts → TestNaiveReference（mig 测试）。
2. 恰好一次：cur 单调递增、每键只在下标 cur 处迁一次、Restart 保留 cur → TestExactlyOnce（mig 测试）。
3. 双路由读写：mig.resolve 按「idx[key] < cur」判定当前分区，Get/Put 都走 resolve → TestDualRoute（api 测试）。
4. 失败不留痕：所有校验先于任何状态修改，哨兵错误即返 → TestRejectsNoSideEffect（api 测试）。
