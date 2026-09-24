# Hinted Handoff 推导（3 副本，maxHints=3，key=k；R1 步前已宕机，初值空@0）

| 步 | 操作 | R0 | R1（宕机期=原值+hints） | R2 | 结果 |
|---|---|---|---|---|---|
| 1 | Write(a,5) | a@5 | (宕)空 + [a5] | a@5 | 在线应用；R1 入队 |
| 2 | Write(b,7) | b@7 | (宕)空 + [a5,b7] | b@7 | 在线应用；R1 入队 |
| 3 | Write(c,6) | b@7 | (宕)空 + [a5,b7,c6] | b@7 | 6<7 在线跳过；宕机仍无条件入队 |
| 4 | Write(d,8) | b@7 | (宕)空 + [a5,b7,c6] | b@7 | **拒绝**：队满 3/3，整体失败不留痕 |
| 5 | Up(1) | b@7 | b@7（队清空） | b@7 | applied=2(a5,b7)，skipped=1(c6) |
| 6 | Down(1);Write(e,9) | e@9 | (宕)b@7 + [e9] | e@9 | 在线应用；R1 入队 |
| 7 | Write(f,9) | e@9 | (宕)b@7 + [e9,f9] | e@9 | 9=9 并列，在线跳过；宕机仍入队 |
| 8 | Up(1) | e@9 | e@9（队清空） | e@9 | applied=1(e9)，skipped=1(f9) |

- **(甲)** 第 4 步若不回滚：R0、R2 已先写成 **d@8**（正确应保持 b@7），hint 侧报错但在线侧污染。
- **(乙)** 第 5 步若无条件按序重放（最后一条胜）：R1 错成 **c@6，版本由 7 降到 6**；严格 `>` 下 c6 跳过，R1=b@7。
- **(丙)** 写与重放都写成 `>=`：第 7 步 f@9 并列覆盖在线副本，第 8 步 f@9 再覆盖 e@9，全副本错成 **f@9**（正确 e@9，并列取最早）。

## 四条不变量的保证位置与钉住测试

1. **朴素参照一致**（最大版本、并列取最早）：`hh/hh.go` Write 对在线副本经 `hint.Apply` 严格 `>` 应用；`hint/hint.go` Replay 同判定。测试：`TestEightStepScenario`、`TestNaiveReference`、`TestConcurrentConvergence`。
2. **重放幂等/收敛**：`hh/hh.go` Up 重放后清空缓冲，紧接 Up 无新 hint。测试：`TestReplayIdempotent`。
3. **版本不回退**：`hint/hint.go` 的 `Apply(hintVer,curVer)` 只在 `hintVer>curVer` 为真；`hh/hh.go` Up 回调复用之。测试：`TestNoDowngrade`。
4. **失败不留痕**：`hh/hh.go` New/Write/Down/Up 全部先校验后改状态，Write 在动任何副本前先扫队满，四类哨兵错误互异。测试：`TestRejectedLeavesNoTrace`。
