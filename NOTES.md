# NOTES — 无锁一致快照读（COW）

## 一、十步操作分步推导表（初态 id=0，空表）

| 步 | 操作 | 操作后 cur.id | 操作后 cur 内容 | 本行读返回值 |
|---|---|---|---|---|
| 1 | Update(A,1) | 1 | {A:1} | — |
| 2 | Update(B,2) | 2 | {A:1,B:2} | — |
| 3 | S = Snapshot() | 2 | {A:1,B:2} | S 句柄绑定版本 2 |
| 4 | Read(A) | 2 | {A:1,B:2} | "1" |
| 5 | Update(A,9) | 3 | {A:9,B:2} | — |
| 6 | Read(A) | 3 | {A:9,B:2} | "9" |
| 7 | S.Read(A) | 3 | {A:9,B:2} | "1"（读版本 2） |
| 8 | ReadKeys([A,B]) | 3 | {A:9,B:2} | {A:9,B:2} |
| 9 | Update(B,5) | 4 | {A:9,B:5} | — |
| 10 | ReadKeys([A,B]) | 4 | {A:9,B:5} | {A:9,B:5} |

**(甲)** 第 7 步 `S.Read(A)` 返回 **"1"**（句柄绑定版本 2）。若原地改共享 map，版本 2 的 map 被第 5 步污染成 A:9，会错返回 **"9"**。

**(乙)** 第 8 步返回 **{A:9,B:2}**（版本 3 整体）。若逐 key 两次独立 Load：第一次 Load 在写前，A 取自版本 3 得 A:9；两次 Load 之间写者依次 Update(A,3)、Update(B,5)，第二次 Load 读到 {A:3,B:5}，B 取 5。撕裂值 = **{A:9,B:5}** —— A 那一半来自真实版本 {A:9,B:2}（id 3），B 那一半来自真实版本 {A:3,B:5}，该组合从未作为完整版本存在过。

**(丙)** 两写者无锁并发、都基于 {A:1} 克隆：甲发布 {A:1,B:2}，乙随后用自己的克隆 {A:1,C:3} 整体覆盖 cur，最终表 **丢失 key B**。正确（写者加写锁串行化）的最终表应为 **{A:1,B:2,C:3}**。

## 二、四条不变量：保证位置与钉住它的测试

1. **与批量参照一致**：写者只在写锁内克隆-修改-原子 Store 发布完整版本（snapshot/snapshot.go `Update`），读只 Load 一次指针。测试 `TestConsistentWithBatchReference`。
2. **快照不可变**：`Update` 必走 `ver.Clone` 生成新 map，旧版本 map 永不写入（ver/ver.go）。测试 `TestSnapshotImmutable`。
3. **读不阻塞写、读永远一致**：读路径零锁，`ReadKeys` 单次 `cur.Load()` 后在同一 `*ver.Version` 上取全部 key（snapshot/snapshot.go）。测试 `TestReadKeysSingleVersion`、`TestConcurrentReadersConsistent`。
4. **失败不留痕**：所有校验（空 k/v、超长 k、超 maxKeys）先于任何状态变更，拒绝时直接返回哨兵错误，id/指针不动（api/api.go）。测试 `TestRejectedOpsLeaveNoTrace`。
