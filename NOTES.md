# NOTES

threshold=2^31（New 合法上限为 2^31-1；本序列无 drop 恰等边界，两者判定与结果完全相同）。

| 步 | r | 事件 | 后 prev | 后 unwrapped |
|---|---|---|---|---|
| 1 | 100 | First | 100 | 100 |
| 2 | 200 | Forward | 200 | 200 |
| 3 | 4294967200 | Forward | 4294967200 | 4294967200 |
| 4 | 50 | Wrap（drop=4294967150） | 50 | 4294967346 |
| 5 | 60 | Forward | 60 | 4294967356 |
| 6 | 40 | 倒退被拒（drop=20） | 60 | 4294967356 |
| 7 | 4294967295 | Forward | 4294967295 | 8589934591 |
| 8 | 5 | Wrap（drop=4294967290） | 5 | 8589934597 |

(甲) 第4步=4294967200+(2^32-4294967200)+50=**4294967346**；公式少算1→4294967345；漏加 r→4294967296。
(乙) 无阈值时第6步误判 Wrap：4294967356+(2^32-60)+40=**8589934632**（正确应拒绝并保持 4294967356）。
(丙) 原始 uint32 比较：第4步 50<4294967200、第8步 5<4294967295 均被误判为倒退异常，实际是回卷前进。原始值跨回卷不单调，无法区分回卷与倒退；不变量1的参照必须用展开后的64位单调值（如 4294967346>4294967200）逐值比较。

不变量 → 代码保证位置 → 钉住测试：

1. 朴素参照一致：`eng/eng.go` Feed 只按当前 prev/pu 单步调用 wrap 的 Classify/Unwrap → `TestNaiveReference`（含随机序列离线重算）。
2. 单调不减：`eng/eng.go` Feed 仅在接受后提交 pu，First/Forward/Wrap 增量为正、Duplicate 增量0、被拒不提交 → `TestMonotone`。
3. 回卷正确：`wrap/wrap.go` Unwrap 的 Wrap 分支 pu+(2^32-prev)+r，溢出先判 → `TestWrapFormula`。
4. 失败不留痕：`api/api.go` New/空态查询先判错；`eng/eng.go` Classify/Unwrap 出错在改 prev/pu/counts 前返回 → `TestSentinelErrors`（溢出路径另由 `TestOverflowRejected` 钉）。
