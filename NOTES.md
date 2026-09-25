# NOTES — 按组 Top-K 增量维护

## 三、七步推导（K=2；全序：分高在前，同分 ItemID 字典序小在前）

| # | 操作 | 现存项（全序，分:号） | 榜内 top2 | 落榜 |
|---|---|---|---|---|
| 1 | Add(g,a,10) | 10:a | a | — |
| 2 | Add(g,b,20) | 20:b 10:a | b a | — |
| 3 | Add(g,c,15) | 20:b 15:c 10:a | b c | a |
| 4 | Add(g,d,15) | 20:b 15:c 15:d 10:a | b c | d a |
| 5 | Add(g,e,20) | 20:b 20:e 15:c 15:d 10:a | b e | c d a |
| 6 | Remove(g,b) | 20:e 15:c 15:d 10:a | e c | d a |
| 7 | Remove(g,e) | 15:c 15:d 10:a | c d | a |

- (甲) 并列若「新到优先」：第4步 d 排到 c 前 → 榜错成 [b,d]；第5步 e 排到 b 前 → 榜错成 [e,b]（正确应为 [b,e]）。
- (乙) 若「落榜即丢」：第5步 c 落榜即被丢弃，第6步删 b 后补不出 c → 榜错成 [e]（正确应为 [e,c]）。
- (丙) 重复 Add 若当「更新分数」：c 被改成 999 → 榜错成 [c:999, b:20]；正确应报 ErrDuplicate，榜仍为 [b:20, c:15]。

## 二、四条不变量的落点

1. 与批量重算一致：topk.Group.Add/Remove 始终维护同一全序（topk/topk.go 的 Less、insertSorted、Remove 补位），top+rest 即全量有序；钉于 TestAgainstBatchRecompute。
2. 全序稳定：topk.Less = 分降序 + ItemID 升序，top 与 rest 各自有序且 top 全体优于 rest，All()=top+rest；钉于 TestTotalOrder。
3. 补位唯一：topk.Group.Remove 删榜内项时取 rest[0]（恰为剩余第 K 名），删落榜项不动榜；钉于 TestPromote。
4. 失败不留痕：groups.Manager.Add/Remove 先校验（空串/重复/不存在）后改状态，api.New 拒 k<=0；钉于 TestRejectedOpsNoSideEffect。
