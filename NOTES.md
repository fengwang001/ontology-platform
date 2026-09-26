# 2PC 协调者状态机 — 推导与不变量

## 一、六步推导（n=3，参与者 0/1/2）

| 步 | 操作 | yes | no | 决定 |
|---|---|---|---|---|
| S1 | Vote(0,true) | 1 | 0 | 未决（未投齐） |
| S2 | Vote(1,true) | 2 | 0 | 未决（未投齐） |
| S3 | Vote(2,false) | 2 | 1 | Abort（已投齐且存在 no） |
| S4 | Decide() | 2 | 1 | Abort |
| S5 | Vote(2,true) 重复投票 | 2 | 1 | 拒绝（ErrAlreadyVoted），状态不变，仍 Abort |
| S6 | 新事务 Vote(0,1)=yes，2 未投，Recover() | 2 | 0 | Abort（安全中止在途事务） |

- (甲) S4 正确决定是 **Abort**（参与者 2 投 no）。若按多数派（yes≥n/2+1=2 即 Commit）会错成 **Commit**——正确应为 Abort，多数派把一票否决权弄丢了。
- (乙) S5 正确实现**拒绝**重复投票（票不可改），决定仍是 **Abort**。若允许改票覆盖，参与者 2 的 no 被改成 yes，yes=3/no=0，决定会错成 **Commit**。
- (丙) S6 正确决定是 **Abort**（参与者 2 始终未投票，Recover 安全中止）。若按「已知 yes 达多数派即 Commit」（2≥2）会错成 **Commit**——正确应为 Abort。安全问题：未投票参与者的意志被无视，崩溃/分区期间会把一个可能有参与者反对或根本不知情的事务提交掉，破坏原子性与全体一致。

## 二、四条不变量的保证位置与钉住它们的测试

1. 与朴素重算一致：`coord.Decide` 用 yes/no 计数判定，等价于全表扫描「全 yes 才 Commit」；测试 `TestDecideMatchesNaive`（api/api_test.go，随机序列对比朴素重算）。
2. 全体一致：`coord.Decide`/`Recover` 仅当 `yes==n` 才 Commit，任一 no 或未投齐一律 Abort；测试 `TestUnanimousRequired`。
3. 票不可改：`pt.State.Cast` 对已投票者返回 `ErrAlreadyVoted` 且不改状态；测试 `TestVoteImmutable`。
4. 失败不留痕：越界/重复投票/未投齐 Decide 在改任何状态前返回哨兵错误（`coord.Vote` 先校验后计数、`pt.State.Cast` 先校验后写）；测试 `TestRejectionsLeaveStateUnchanged`。
