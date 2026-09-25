# NOTES

## K=3，同 Key 六次 Put 分步表
| 步 | 操作 | 分配 v | 物理保留（旧→新） | 清理 |
|---|---|---|---|---|
| 1 | Put("a") | 1 | 1:a | 无 |
| 2 | Put("b") | 2 | 1:a, 2:b | 无 |
| 3 | Put("c") | 3 | 1:a, 2:b, 3:c | 无（计数 3，不 >3） |
| 4 | Put("d") | 4 | 2:b, 3:c, 4:d | 删 v1 |
| 5 | Put("e") | 5 | 3:c, 4:d, 5:e | 删 v2 |
| 6 | Put("f") | 6 | 4:d, 5:e, 6:f | 删 v3 |

- （甲）第 3 步后保留 3 个、不删（正确条件是 >K）。若误为 >=K：第 3 步即错删 v1，`GetAt(1)` 错成「不存在」（正确应命中 "a"）。
- （乙）第 4 步后最旧保留版本是 v2，`GetAt(2)` 命中 "b"。若命中条件误为 `v > 最旧保留`（严格大于）：v2 被排除，错成「不存在」。
- （丙）第 4 步后 `GetAt(1)` 应为「不存在」（v1 已物理删除）。若清理惰性（只标记、物理仍在，GetAt 按物理存在判断）：错返回 "a"。

## 第二节四条不变量：代码位置与钉住的测试
1. 与朴素重放一致、保留数 <=K：`store.Put` 只追加不改动旧值（store.go），最新值即 hist 环尾 `Latest`；`TestReplayConsistency`。
2. 恰留最新 K 个、版本号连续单调永不复用：`hist.Append` 满则头指针前移一格 O(1) 弃旧（hist.go），版本由 `store.Put` 按 `MaxVersion()+1` 分配（store.go）；`TestRetentionWindow`、`TestSixPutTable`。
3. GetAt 命中当且仅当在 [minV,maxV] 窗口内：`hist.At`（hist.go）经 `store.GetAt` 暴露；`TestGetAtVisibility`。
4. 失败不留痕：`store.New`/`Put`/`GetAt` 全部先校验后触碰状态（store.go），哨兵 `ErrBadLimit`/`ErrEmptyKey`/`ErrInvalidVersion` 三者互异；`TestRejectedOpsNoTrace`。

O(1) 清理计数 `lastCleanupMoves` 为 hist 非导出字段，仅包内测试可读：`TestPutCleanupMovesBounded`；并发由 store 单把 RWMutex 保证：`TestConcurrentReaders`。
