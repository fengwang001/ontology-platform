# NOTES

## 六步推导（初始 state 为空；“集”=已应用集）

| 步 | 操作 | 在集? | 动作 | 本步后 state | 本步后已应用集 |
|---|---|---|---|---|---|
| 1 | Apply(1,k,+5) | 否 | 应用 | {k:5} | {1} |
| 2 | Apply(2,k,+3) | 否 | 应用 | {k:8} | {1,2} |
| 3 | Apply(1,k,+5) | 是 | 跳过 | {k:8} | {1,2} |
| 4 | Apply(3,k,+5) | 否 | 应用 | {k:13} | {1,2,3} |
| 5 | Apply(2,k,+3) | 是 | 跳过 | {k:13} | {1,2,3} |
| 6 | Apply(4,m,+2) | 否 | 应用 | {k:13,m:2} | {1,2,3,4} |

(甲) 第4步 txid=3 首次出现，按 txid 去重必须**应用**；若按内容 (key,delta) 去重，(k,+5) 在第1步见过，会被错判**跳过**，state[k] 错成 **8**（正确 **13**）。
(乙) state[k]=13 时两 goroutine 并发 Apply(5,k,+2)：正确只应用一次，state[k]=**15**；查重与应用非原子则双双过查重、+2 两次，错成 **17**。
(丙) 容量淘汰丢失 txid1，Restore{2,3,4} 后重投 Apply(1,k,+5) 被当新事务再应用，state[k] 错成 **18**（正确 **13**），违反**不变量 1**（重启后同一事务被应用两次，连带违反 3）。

## 不变量落点

1. 不重复应用：`apply/apply.go` 的 Apply 在同一把 mutex 内完成 Seen→改 state→Add；测试 TestNoDuplicateApply、TestConcurrentApply 钉住。
2. 与朴素参照一致：同上临界区，仅首见 txid 执行 state[key]+=delta；测试 TestNaiveReference 钉住。
3. 幂等：Seen 命中立即返回且不触碰 state/集合；测试 TestIdempotentRepeat（含不同内容重投）钉住。
4. 失败不留痕：apply.go 中 txid 在锁外先判，key/delta 在锁内查重未命中后、改 state 前判（故拒绝不动 state/集），Restore 先全量校验再整体换集；测试 TestRejectedLeavesNoTrace、TestRestoreRejectsIllegal 钉住。
