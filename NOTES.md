# NOTES：(分区,位点) 高水位幂等 sink

规则：`Offset<=W[p]` 判重丢弃并计数；`Offset>W[p]` 生效（table[Key]+=Val）并把 W[p] 推到 Offset；未生效分区 W=-1。

| 记录 | 判定 | W[0] | W[1] | table[a] | table[b] | 累计重复 |
|---|---|---|---|---|---|---|
| r1 (0,0,a,1) | 生效 | 0 | -1 | 1 | 0 | 0 |
| r2 (0,1,a,2) | 生效 | 1 | -1 | 3 | 0 | 0 |
| r3 (1,0,b,5) | 生效 | 1 | 0 | 3 | 5 | 0 |
| r4 (1,1,b,3) | 生效 | 1 | 1 | 3 | 8 | 0 |
| r5 (0,1,a,2) | 重复(Offset==W[0]) | 1 | 1 | 3 | 8 | 1 |
| r6 (0,3,a,4) | 生效 | 3 | 1 | 7 | 8 | 1 |
| r7 (1,1,b,3) | 重复(Restart 后 Offset==W[1]) | 3 | 1 | 7 | 8 | 2 |
| r8 (0,3,a,4) | 重复(Offset==W[0]) | 3 | 1 | 7 | 8 | 3 |
| r9 (1,2,a,6) | 生效 | 3 | 2 | 13 | 8 | 3 |
| r10 (0,4,b,8) | 生效 | 4 | 2 | 13 | 16 | 3 |

最终：W[0]=4，W[1]=2，table[a]=13，table[b]=16，重复数=3（Restart 夹在 r6、r7 之间，状态原样重建）。
（甲）r5/r7/r8 都是 Offset==W（1==W[0]、1==W[1]、3==W[0]）。若判重写成 Offset<W，三条全被误生效：a=13+2+4=19，b=16+3=19，重复数=0。
（乙）只维护一个全局水位时 r3(0≤1)、r4(1≤1)、r9(2≤3) 被误判为重复：a=7（r1+r2+r6=1+2+4，r9 丢失），b=8（仅 r10=8；r3、r4 丢失）。
（丙）水位不随批持久、Restart 后回 -1（表保留）：批3 中 r7、r8 被重复生效（r9、r10 仍正常）：a=13+4=17，b=16+3=19；违反不变量 3（重启无关）。

不变量保证位置 / 钉住测试：
1. 与朴素参照一致：`wm.Dup` 的 `off<=w` 判定 + `sink.Commit` 逐条顺序生效（wm/wm.go、sink/sink.go）→ TestNaiveReferenceRandom。
2. 分区独立：水位按分区存 `durable.marks map[int]int64`，提交副本只更新本记录所属分区（sink/sink.go）→ TestPartitionIndependence。
3. 重启无关：Restart 从持久区 table/marks/dups 三份状态直接重建，不重放记录（sink/sink.go Restart）→ TestRestartInvariance。
4. 失败不留痕：`wm.Validate` 与分区数检查全部通过后才在副本上提交并整体替换（sink/sink.go Commit）→ TestRejectedBatchLeavesNoTrace。
