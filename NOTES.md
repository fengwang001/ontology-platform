# 多副本反熵读修复 — 推导与不变量锚点

## 八步分步表（条目写法 value@ver，∅=空）

| 步 | 操作 | R0 | R1 | R2 | winner | 回填 | repaired |
|---|---|---|---|---|---|---|---|
| 1 | Put(k,"a",5,{0,1}) | a@5 | a@5 | ∅ | — | — | — |
| 2 | Read(k) | a@5 | a@5 | a@5 | a@5 | R2 | 1 |
| 3 | Put(k,"b",7,{1,2}) | a@5 | b@7 | b@7 | — | — | — |
| 4 | Put(k,"c",7,{2}) | a@5 | b@7 | c@7 | — | — | — |
| 5 | Read(k) | c@7 | c@7 | c@7 | c@7 | R0,R1 | 2 |
| 6 | Put(k,"d",4,{0}) | d@4 | c@7 | c@7 | — | — | — |
| 7 | Read(k) | c@7 | c@7 | c@7 | c@7 | R0 | 1 |
| 8 | Read(k) | c@7 | c@7 | c@7 | c@7 | 无 | 0 |

- (甲) 漏回填空副本：第 2 步 repaired 错成 0，R2 错留 ∅（应为 (a,5)）。
- (乙) 严格 `>` 并列保留下标最小者：winner 错成 "b"@7（R1），R0 与 R2 都被回填成 (b,7)。
- (丙) 按 Put 到达时间判定：第 7 步错返回 "d"，R1、R2 被回填成 (d,4)，版本从 7 降到 4。

## 四条不变量的保证位置与钉住测试

1. 与朴素参照一致：`rep.Winner`（最大版本+并列取字典序更大 value）与 `rrep.Store.Read` 回填循环；测试 `TestNaiveConsistency`。
2. 修复收敛：`rrep.Store.Read` 把空/落后/冲突副本一律改写为 winner；测试 `TestRepairConverges`。
3. 只升不降：回填写入的恒为最大版本的 winner，且 `rep.Beats` 只认版本与字典序；测试 `TestRepairNeverDowngrades`。
4. 失败不留痕：`api` 层先做全部校验（`New`/`Put`/`Read` 入口）再触碰 `rrep`；测试 `TestRejectedOpsNoSideEffect`、`TestErrorsDistinct`。
