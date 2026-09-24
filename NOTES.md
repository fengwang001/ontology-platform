# NOTES — 增量 FULL OUTER JOIN

## 第三节：八步推导（Key=K，`-` 表示 NULL 补位）

| 步 | 操作 | 左集 | 右集 | 本步变更日志 | 视图行数 |
|---|---|---|---|---|---|
| 1 | PutLeft(a) | {a} | {} | +[K,a,-] | 1 |
| 2 | PutRight(x) | {a} | {x} | -[K,a,-]; +[K,a,x] | 1 |
| 3 | PutRight(y) | {a} | {x,y} | +[K,a,y] | 2 |
| 4 | PutLeft(b) | {a,b} | {x,y} | +[K,b,x]; +[K,b,y] | 4 |
| 5 | DelLeft(a) | {b} | {x,y} | -[K,a,x]; -[K,a,y] | 2 |
| 6 | DelLeft(b) | {} | {x,y} | -[K,b,x]; -[K,b,y]; +[K,-,x]; +[K,-,y] | 2 |
| 7 | DelRight(x) | {} | {y} | -[K,-,x] | 1 |
| 8 | DelRight(y) | {} | {} | -[K,-,y] | 0 |

- (甲) 第 2 步正确输出：先 `- [K,a,-]` 撤回左 NULL 补位行，再 `+[K,a,x]`。若忘撤回，视图错成 **2 行**，多出的是 `[K,a,-]`。
- (乙) 第 6 步正确输出：`-[K,b,x]`、`-[K,b,y]`，再补 `+[K,-,x]`、`+[K,-,y]`。若只撤回配对，视图错成 **0 行**，漏掉 `[K,-,x]` 与 `[K,-,y]`。
- (丙) 第 5 步若误发 `-[K,a,-]`：撤回了一个视图中不存在的值（违反不变量 2），且 `[K,a,x]`、`[K,a,y]` 漏撤回，视图错成 **4 行**（应为 2）。

## 第二节：四条不变量的保证位置与钉住测试

1. **与批量重算一致**：`rel.Rel.View` 按 FULL OUTER JOIN 语义对两侧多重集重算，`foj.Engine.View` 逐键拼接；`api.checkSeq` 用独立参考实现逐步比对。测试：`TestViewMatchesBatchRecompute`。
2. **变更日志自洽**：`rel.put/del` 只产出当前视图中真实存在的行的增删（先校验后改状态）；`api.checkSeq` 用影子视图按序应用每条日志并校验。测试：`TestChangelogSelfConsistent`。
3. **穿越才切换**：NULL 补位与配对行的切换只出现在 `rel.put`/`rel.del` 的 `m0==0`/`t0==0` 分支，其余分支只增删对应行。测试：`TestCrossingOnlySwitch`。
4. **失败不留痕**：`rel` 先校验（重复/不存在）后改状态；`foj.Apply` 备份受影响键、任一 op 出错整体回滚并返回 nil 日志。测试：`TestFailureAtomicity`。
