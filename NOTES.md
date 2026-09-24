# 在线 schema 迁移推导笔记

## 九步操作分步表（每行：操作后的阶段 / v1 全表 / v2 全表）

| # | 操作 | 阶段 | v1 | v2 |
|---|------|------|----|----|
| 1 | P("a",3) | Normal | a:3 | — |
| 2 | P("b",5) | Normal | a:3 b:5 | — |
| 3 | B | DualWrite | a:3 b:5 | — |
| 4 | P("c",4) | DualWrite | a:3 b:5 c:4 | c:8 |
| 5 | F | DualWrite | a:3 b:5 c:4 | a:6 b:10 c:8 |
| 6 | F | DualWrite | a:3 b:5 c:4 | a:6 b:10 c:8 |
| 7 | P("a",7) | DualWrite | a:7 b:5 c:4 | a:14 b:10 c:8 |
| 8 | S | Switched | a:7 b:5 c:4 | a:14 b:10 c:8 |
| 9 | P("d",2) | Switched | a:7 b:5 c:4 | a:14 b:10 c:8 d:4 |

- (甲) 若回填每次对 v2 再乘 2：第 6 步后 a 错成 12（应 6）、b 错成 20（应 10）。
- (乙) Switch 前 Get("a")=7（读 v1），Switch 后 =14（读 v2）。若 DualWrite 阶段就读 v2，第 7 步后 Get("a") 错成 14（应 7）。
- (丙) 若第 4 步双写不原子（v2 缺 c），且 c 已被标记已回填而跳过重扫，Switch 后 Get("c") 读 v2 缺失错成 0（应 8）。

## 四条不变量的保证位置与钉住测试

1. 与批量重算一致：`ddl.Store.Get` 按阶段分派读 v1/v2；测试 `TestGetMatchesBatchModel`（api 包，随机序列对拍朴素模型）。
2. 双写一致：`ddl.Store.Put` 在 DualWrite 下同一临界区先校验故障再同时写两表，故障时直接返回不写任何表；测试 `TestDualWriteConsistency`。
3. 回填幂等：`ddl.Store.Backfill` 恒写 `v2[k]=2*v1[k]`（从不基于旧 v2），并用已回填集跳过；测试 `TestBackfillIdempotent`。
4. 失败不留痕：所有校验（阶段/键/值/故障）都在任何写之前完成，哨兵错误直接返回；测试 `TestRejectedOpsLeaveNoTrace`。
