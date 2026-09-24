# 推导
| 步骤 | 操作 | Spans() |
|---|---|---|
| 1 | Add[1,3) | [1,3) |
| 2 | Add[3,5) | [1,5) |
| 3 | Add[7,9) | [1,5),[7,9) |
| 4 | Add[5,5) | [1,5),[7,9) |
| 5 | Add[5,7) | [1,9) |
| 6 | Remove[2,8) | [1,2),[8,9) |

合并式必须是按 lo 排序后的 `a.hi >= b.lo`：第 2 步 `5>=3` 会合并；第 3 步 `5>=7` 为假，不合并。写成 `a.hi > b.lo` 会在第 2 步留下 `[1,3),[3,5)`，违反“相邻者必须已合并”。
[5,5) 在第 4 步会按 `a.hi>=b.lo` 被判为与 `[1,5)` 端点贴合；若让空区间进入插入/合并路径，零长度结果可能留下 `[5,5)` 或污染普通区间合并，违反“不存在空区间”。因此空区间必须在合并前静默忽略：它不含任何整点，合法但不改变点集。

# 不变量位置与测试
1. 规范形：`set.Add` 的 staged/canonical 重写；`TestCanonicalAndSplitting`、`TestAPISelfCheck`。
2. 逐点一致：`set.Add`/`set.Remove` 与整数点集合同构；`TestNaiveEquivalence`、`TestAPISelfCheck`。
3. 移除分裂：`set.Remove` 保留左右残段并跳过零长度段；`TestCanonicalAndSplitting`。
4. 失败不留痕：哨兵错误在提交前返回，`api.SelfCheck` 复核；`TestFailuresAtomic`、`TestAPISelfCheck`。
