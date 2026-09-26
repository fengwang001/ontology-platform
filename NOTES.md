# 2PC 协调者状态机 NOTES

## 一、六步推导（n=3，参与者 0/1/2）

| 步 | 操作 | yes | no | 决定 |
|---|---|---|---|---|
| S1 | Vote(0,true) | 1 | 0 | 未决（未投齐） |
| S2 | Vote(1,true) | 2 | 0 | 未决（未投齐） |
| S3 | Vote(2,false) | 2 | 1 | 未决（已投齐但尚未 Decide） |
| S4 | Decide() | 2 | 1 | Abort（存在 no） |
| S5 | Vote(2,true) | 2 | 1 | 拒绝：重复投票，状态不变，决定仍 Abort |
| S6 | 新事务 Vote(0,true);Vote(1,true);Recover() | 2 | 0 | Abort（2 未投，安全中止） |

- (甲) S4 正确决定是 **Abort**。若按多数派（yes≥n/2+1=2 即 Commit），会错成 **Commit**——一票 no 被吞掉，违反全体一致。
- (乙) S5 正确实现**拒绝**该票（ErrDuplicateVote），状态不变，决定仍是 **Abort**。若允许改票覆盖，yes=3/no=0，会错成 **Commit**。
- (丙) S6 正确决定是 **Abort**（有参与者未投票，安全中止在途事务）。若按「已知 yes 达多数派即 Commit」，yes=2≥2 会错成 **Commit**——把未表态参与者的沉默当成默许，崩溃恢复期间可能提交一个参与者根本没同意、甚至投了 no 后失联的事务，违反原子性。

## 二、四条不变量：保证位置与钉住测试

1. 与朴素重算一致：`coord.Decide` 用增量 yes/no 计数判定，与 `naive` 扫描同规则；测试 `TestDecideMatchesNaive`（随机序列对拍）。
2. 全体一致：`coord.Decide`/`Recover` 仅当 `yes==n && no==0` 才 Commit；测试 `TestUnanimousOnly`。
3. 票不可改：`pt.Ballot.Cast` 对已投票返回 ErrDuplicateVote 且不改值；测试 `TestVoteImmutable`。
4. 失败不留痕：`coord.Vote/Decide` 先校验后写，错误路径零副作用；测试 `TestRejectedOpsLeaveNoTrace`。
