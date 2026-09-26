# NOTES

推导：日志 1:Add(2) 2:Mul(3) 3:Add(1) 4:Add(5)，state/lastApplied/committed 初始均为 0。

| 步 | committed | lastApplied | State |
|---|---|---|---|
| S1 Append 四条 | 0 | 0 | 0 |
| S2 Commit(3) | 3 | 0 | 0 |
| S3 Apply() | 3 | 3 | 7 |
| S4 Restart(2,6) 且 Commit(4) | 4 | 2 | 6 |
| S5 Apply() | 4 | 4 | 12 |

(甲) S3 正确为 7：(0+2)*3+1=7。倒序应用（3→1）得 (0+1)*3+2=5，错成 5；体现 Add 与 Mul 不交换、复合不可换序（命令顺序敏感），故必须按下标升序应用。
(乙) 若终止条件错成日志末尾下标 4，会多应用未提交的第 4 条 Add(5)，得 12，正确为 7；错在第 4 条 Add(5)——只能应用到 committed=3。
(丙) Restart(2,6) 后 lastApplied 应为 2；Apply() 只应用第 3、4 条，State=(6+1)+5=12。若误把 lastApplied 重置为 0，则从快照态 6 重放 1..4：((6+2)*3+1)+5=30，错成 30，正确为 12。

不变量保证位置 / 钉住的测试函数：

- I1 重算一致：sm.Apply 为纯函数，repl.go 的 Apply 仅从 state 续算 lastApplied+1..committed — TestRecomputeConsistency
- I2 索引自洽：repl.go 的 Append/Commit/Apply/Restart 全程维持 0<=lastApplied<=committed<=len(log) — TestIndexInvariant
- I3 确定性收敛：升序逐条应用 + Restart 快照定位（repl.go），分批不改变结果 — TestConvergence
- I4 失败不留痕：repl.go 三个方法先校验、通过后才改状态 — TestRejectedOpsAtomic
- 复杂度：非导出 repl.reads 计最近一次 Apply 读取条数，从 lastApplied 续读 — TestIncrementalReadCount
- 五步场景 api 复算 — TestFiveStepScenario；并发只读一致 — TestConcurrentReaders；自检 — TestSelfCheck
