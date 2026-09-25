# 按位点断点续传累加器 — 推导与不变量

## 一、七步分步表（初始 cp=-1，sum[k]=0，pending=∅）

| 步 | 操作 | 之后 pending 的 offset 集合 | 之后 cp | 之后 sum[k] |
|---|---|---|---|---|
| 1 | Apply(k,0,+10) | {0} | -1 | 0 |
| 2 | Apply(k,2,+20) | {0,2} | -1 | 0 |
| 3 | Apply(k,3,+30) | {0,2,3} | -1 | 0 |
| 4 | Commit()：0 连续可提交，1 缺口停 | {2,3} | 0 | 10 |
| 5 | Apply(k,2,+20)：2 在 pending，幂等跳过 | {2,3} | 0 | 10 |
| 6 | Apply(k,1,+40) | {1,2,3} | 0 | 10 |
| 7 | Commit()：1,2,3 连续提交 | ∅ | 3 | 100 |

**(甲)** 第 4 步后正确值 cp=0、sum[k]=10。若错把 cp 设为 pending 中最大 offset：cp 错成 3、sum 错成 60（0,2,3 被全量累加，缺口 1 被跳过）；随后 `Apply(k,1,+40)` 因 `1<=cp=3` 被误判「已持久化」而幂等跳过，+40 永久丢失，最终 sum[k] 错成 60（正确 100）。

**(乙)** 只查 `offset<=cp`、不查 pending，且 pending 用追加切片（不去重）：第 5 步重复的 offset 2 被再次追加；第 7 步 Commit 对连续前缀里每个匹配条目都累加，1(+40)、2(+20)、2(+20)、3(+30) 全计入，sum[k] 错成 120（正确 100）。

**(丙)** 第 4 步后 Restore：sum[k]=10 与 cp=0 保留（持久化），pending{2,3} 丢失（易失）。源端从 cp+1=1 重投 offset 1、2、3，重放后 sum[k]=10+40+20+30=100，精确恢复。若 cp 也未持久化（重启复位 -1），源端会从 0 起多投 offset 0，其 +10 被再次接受，重放后 sum[k] 错成 110（正确 100）。

## 二、四条不变量：保证位置 / 钉住测试

1. **与朴素参照一致**：`acc.Commit` 只经 `ckpt.Fold` 折叠连续前缀，`acc.Apply` 只收新 offset；测试 `TestNaiveReference`（随机 Apply/Commit 交错对比朴素模型）。
2. **连续性**：`cp` 唯一推进点是 `ckpt.Fold`，遇第一个缺口即停、绝不跳过；测试 `TestCommitStopsAtGap` 与 `TestNaiveReference`。
3. **幂等/精确恢复**：`acc.Apply` 用 `ckpt.Decide` 双判据（`offset<=cp` 或在 pending 中）幂等跳过；`acc.Restore` 仅清 pending；测试 `TestIdempotentAndRestore`。
4. **失败不留痕**：`acc.Apply` 先完成空 Key / 负 offset / 超 maxPending 三项校验再改状态，三类哨兵错误互不相同；测试 `TestApplyErrorsNoSideEffect`。
