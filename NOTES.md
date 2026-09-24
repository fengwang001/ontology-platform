# CDC 位点提交器 NOTES

## 第三节推导：Assign(0,100)，Deliver 100..107，Ack 序列 103,100,101,105,101,102,107

| 步 | Ack | 已 Ack 未被提交覆盖的位点集合 | Committed(0) |
|---|---|---|---|
| 1 | 103 | {103} | 100 |
| 2 | 100 | {103} | 101 |
| 3 | 101 | {103} | 102 |
| 4 | 105 | {103,105} | 102 |
| 5 | 101（重复，幂等） | {103,105} | 102 |
| 6 | 102 | {105} | 104 |
| 7 | 107 | {105,107} | 104 |

- (甲) 第 6 步后 Committed(0)=**104**。若错把 C 当「最后处理完的位点」提交，此刻提交成 **103**，重启后从 103 读 → **103 被多重复一条**。若提交值正确（104）但重启方错从 C+1=105 读 → **104 被跳过**，违反第二节**不变量 1（至少一次）**。
- (乙) C=「已 Ack 最大位点+1」：第 1 步 Ack(103) 后即提交成 **104**；此刻崩溃重启 → **100、101、102 三条丢失**（已投递却永不再投递、也未处理）。
- (丙) 七个 Ack 后 Restart：C=104，重启后重复投递 **{104,105,106,107}**；其中**已处理过**的是 **105、107**（至少一次语义的重复），104、106 尚未处理。第 5 步重复 Ack(101) 幂等，**无影响**。

## 四条不变量：保证位置与钉住测试

1. 至少一次：`ofs.(*State).Ack` 推进循环只在 `acked` 命中时才前移 C（ofs/ofs.go）；测试 `TestNaiveConsistency`（校验 `[起点,C)` 全部已 Ack）。
2. 极大性：同一循环遇到第一个未 Ack 位点即停（ofs/ofs.go）；测试 `TestNaiveConsistency`（C 等于朴素参照，不能再大一步）。
3. 朴素一致：随机交错 Deliver/Ack 序列对照朴素参照（api.SelfCheck 同逻辑）；测试 `TestNaiveConsistency`、`TestRestartRedelivery`。
4. 失败不留痕：`cmt.(*Committer).Deliver/Ack` 先校验（ErrUnassigned/ErrTooManyInFlight）再调用 ofs，ofs 内 Deliver/Ack 也是先校验后变更（cmt/cmt.go、ofs/ofs.go）；测试 `TestFailuresLeaveNoTrace`。

复杂度：`ofs.State.checked` 非导出计数器，推进只在 C 被 Ack 时发生、不做整表重扫；测试 `ofs.TestAdvanceCheckCount`。并发：`cmt` 互斥锁；测试 `TestConcurrentAck`（`go test -race`）。
