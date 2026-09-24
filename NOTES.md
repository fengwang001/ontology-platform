# 在线 schema 迁移 NOTES

## 九步推导（P=Put B=BeginMigration F=Backfill S=Switch；每行记录该操作之后的状态）

| # | 操作 | 阶段 | v1 | v2 |
|---|------|------|----|----|
| 1 | P(a,3) | Normal | a:3 | (空) |
| 2 | P(b,5) | Normal | a:3 b:5 | (空) |
| 3 | B | DualWrite | a:3 b:5 | (空) |
| 4 | P(c,4) | DualWrite | a:3 b:5 c:4 | c:8 |
| 5 | F | DualWrite | a:3 b:5 c:4 | a:6 b:10 c:8 |
| 6 | F | DualWrite | a:3 b:5 c:4 | a:6 b:10 c:8 |
| 7 | P(a,7) | DualWrite | a:7 b:5 c:4 | a:14 b:10 c:8 |
| 8 | S | Switched | a:7 b:5 c:4 | a:14 b:10 c:8 |
| 9 | P(d,2) | Switched | a:7 b:5 c:4（冻结） | a:14 b:10 c:8 d:4 |

- (甲) 若回填不幂等（每次 v2[k]=2·v2[k]），第 6 步后 v2 中 a 错成 12、b 错成 20（c 也会错成 16）。
- (乙) 第 8 步 Switch 前 Get(a)=7（读 v1），Switch 后 Get(a)=14（读 v2）。若 DualWrite 阶段就让 Get 读 v2，第 7 步后 Get(a) 会错成 14（应为 7）。
- (丙) 若第 4 步双写不原子（v1 写了 c=4、v2 没写），Switch 后 Get(c) 读 v2 键不存在，错成 0（应为 8）。

## 四条不变量的保证位置与钉住它们的测试

1. 与批量重算一致：`ddl.Store.Get` 按当前阶段选表（Switched 读 v2，否则读 v1），见 ddl.go 的 `Get`；测试 `TestNineStepTrace`。
2. 双写一致：`Put` 在 DualWrite 阶段于同一临界区内同写两表，故障注入时先返回 `ErrDualWrite` 什么都不写，见 ddl.go 的 `Put`；测试 `TestDualWriteAtomic`。
3. 回填幂等：`Backfill` 恒写 `v2[k]=2·v1[k]` 且用 `backfilled` 集合跳过已处理键，见 ddl.go 的 `Backfill`；测试 `TestBackfillIdempotent`。
4. 失败不留痕：所有校验（阶段/键/值/故障）都在任何写之前返回哨兵错误，见 ddl.go 的 `Put`/`BeginMigration`/`Backfill`/`Switch` 开头；测试 `TestRejectedOpsLeaveNoTrace`。
