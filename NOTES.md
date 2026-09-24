# NOTES — EXCEPT ALL 增量物化视图

十行表（New(100)，逐条；视图仅列非零行）：

| # | 变更 | cntL | cntR | out | 本条输出 | 此时视图 |
|---|---|---|---|---|---|---|
| 1 | R +1 a | 0 | 1 | 0 | 无 | {} |
| 2 | L +1 a | 1 | 1 | 0 | 无 | {} |
| 3 | L +1 a | 2 | 1 | 1 | {a,+1} | {a:1} |
| 4 | L +2 a | 4 | 1 | 3 | {a,+2} | {a:3} |
| 5 | R +1 a | 4 | 2 | 2 | {a,−1} | {a:2} |
| 6 | R +3 a | 4 | 5 | 0 | {a,−2} | {} |
| 7 | L −1 a | 3 | 5 | 0 | 无 | {} |
| 8 | R −4 a | 3 | 1 | 2 | {a,+2} | {a:2} |
| 9 | L +1 b | 1 | 0 | 1 | {b,+1} | {a:2,b:1} |
| 10 | R +1 b | 1 | 1 | 0 | {b,−1} | {a:2} |

(甲) 第3步 a=1，最终 a=2。集合语义 EXCEPT：第3步因 cntR≠0 得 0（应为1）；最终 L3,R1 得 0（应为2）。
(乙) 不做 max(0,·)：第6步输出 {a,−3}（应 −2），第7步输出 {a,−1}；第7步后视图 a=−2，违反不变量2（非负）；十步终视图 {a:2} 仍正确，错在中间前缀。
(丙) 第1步正确输出「无」但 cntR 记 1。若 cntL=0 时忽略 R：第2步错输出 {a,+1}（应无）；第5步 R 才起记、第8步 R−4 归零，最终 a=max(0,3−0)=3（应为2）。

不变量保证位置 / 钉住测试：

1. 前缀与批量重算一致：`exc/exc.go` Apply 中 oldOut/newOut=max(0,L−R)，按行增量提交 → TestPrefixConsistency。
2. 视图非负、零不出现：`exc/exc.go` Apply 的 max(0,·) 与 newOut==0 时 delete(view) → TestViewNonNegative。
3. 变更最小：`exc/exc.go` Apply 每条至多一条非零输出且 |Δout|≤|Δin| → TestMinimalChange。
4. 失败不留痕：`exc/exc.go` Apply 先校验后落盘（单条）；`api/api.go` Apply 批内失败时逆序回放逆变更并丢弃输出 → TestRejectedNoTrace、TestRejectedBatchNoTrace；三类哨兵互异 → TestSentinelErrors。
