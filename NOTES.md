# NOTES：两阶段原子提交器推导与不变量

## 十三行分步表（初始 C=0，store=∅；store 只列变化的键）

| # | 操作 | store（操作后） | C | 结果 |
|---|---|---|---|---|
| 1 | Apply(1,10) | {1:10} | 0 | 成功 |
| 2 | Commit(1) | {1:10} | 1 | 成功 |
| 3 | Apply(2,20) | {1:10,2:20} | 1 | 成功 |
| 4 | Apply(2,20) | {1:10,2:20} | 1 | 成功（幂等覆盖，值不变） |
| 5 | Restart() | {1:10,2:20} | 1 | 成功，Pending=[2] |
| 6 | Apply(2,20) | {1:10,2:20} | 1 | 成功（崩溃后重放，仍幂等） |
| 7 | Commit(2) | {1:10,2:20} | 2 | 成功 |
| 8 | Commit(4) | {1:10,2:20} | 2 | ErrOffsetJump（4>C+1=3） |
| 9 | Apply(3,30) | {1:10,2:20,3:30} | 2 | 成功 |
| 10 | Commit(3) | {1:10,2:20,3:30} | 3 | 成功 |
| 11 | Commit(4) | {1:10,2:20,3:30} | 3 | ErrEffectMissing（store 无 4） |
| 12 | Apply(4,40) | {1:10,2:20,3:30,4:40} | 3 | 成功 |
| 13 | Commit(4) | {1:10,2:20,3:30,4:40} | 4 | 成功 |

## 三问

- (甲) 颠倒为「先位点后效果」：Commit(2) 使 C=2 后崩溃，重启 C=2、store 无 2，事件 2 的效果（20）永久丢失（C 已宣称 2 已提交，Pending 不会再报它）。正确顺序下崩溃点处效果已落盘：C=1、store 有 2，Restart 后 Pending=[2]，事件 2 只会被重复处理，不丢。
- (乙) 第 8 步正确实现返回 ErrOffsetJump，C 保持 2。若不检测跳跃、允许批量提交，C 会被推到 4；此时 store 里没有 3，事件 3 的副作用从未写入，且 3≤C 使 Pending 永不报告它——事件 3 永远不再被处理，效果静默丢失。
- (丙) 若 Apply 是累加，第 4、6 步两次 Apply(2,20) 后 store[2]=40（应为 20）。说明「先效果后位点」的至少一次语义必须配套幂等效果（覆盖语义），否则重放会重复计入。

## 四条不变量：保证位置与钉住测试

1. 不丢：Commit 仅在 store[C+1] 存在时推进 C（txn.Txn.Commit 的存在性检查）；测试 TestNoLossVsNaive（随机序列对照朴素参照）。
2. 位点连续：Commit 对 seq>C+1 一律 ErrOffsetJump，C 只 +1（txn.Txn.Commit 分支）；测试 TestOffsetJumpRejected。
3. 幂等重放：Apply 为覆盖写（eff.Store.Put），seq<=C 的 Apply/Commit 为幂等空操作；测试 TestIdempotentReplay。
4. 失败不留痕：所有拒绝分支在写 store/C 之前返回错误（txn.Txn.Apply/Commit 前置校验）；测试 TestFailureLeavesNoTrace。
