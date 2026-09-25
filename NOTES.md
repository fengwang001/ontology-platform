# 变更日志校验和：推导与不变量

e(Seq,Val)=100*Seq+Val，segSize=3；段1=Seq1..3，段2=Seq4..6。

| 步 | 操作 | seg1 | seg2 | total |
|---|---|---|---|---|
| 1 | Append(1,10) e=110 | 110 | 0 | 110 |
| 2 | Append(2,20) e=220 | 330 | 0 | 330 |
| 3 | Append(3,30) e=330 | 660 | 0 | 660 |
| 4 | Append(4,40) e=440 | 660 | 440 | 1100 |
| 5 | Append(5,50) e=550 | 660 | 990 | 1650 |
| 6 | Append(6,60) e=660 | 660 | 1650 | 2310 |
| 7 | 损坏4(Val→70)、5(Val→80)后 Recompute | 660 | 1710 | 2370 |
| 8 | Verify(1,6) 定位 | — | — | 首损=4 |

(甲) 仅损 Seq5：闭区间 [1,5] 返回 corrupt=5, ok=false；左闭右开 [1,5) 只扫 1..4，漏掉 Seq5，误报成 ok=true（无损坏）。
(乙) e 错写成 Seq+Val：全局总量=11+22+33+44+55+66=231（正确 2310），段2=44+55+66=165（正确 1650）。
(丙) 两条损坏：升序第一条=Seq4；按 Seq 降序扫描会错报成 Seq5。

## 不变量保证位置 / 钉住的测试
1. 与朴素参照一致：verify.Engine.Verify 对区间升序逐条 cksum.Elem 重算比对；TestVerifyMatchesNaive。
2. 总量自洽：Append 在同一把锁内 seg[k]+=e 且 total+=e；Recompute 后 total=Σseg；TestTotalInvariant。
3. 定位正确：Verify 仅在合法 [from,to] 内升序扫描、首个不匹配即返回；TestVerifyLocatesFirst。
4. 失败不留痕：New/Append/Verify/Recompute 全部先校验、通过后才改状态；TestRejectionLeavesState。
复杂度：lastCmp 为非导出字段，TestVerifyCounterBounded（N=100/1000/10000，小区间 10 条，计数恒为 10）。
并发：TestConcurrentVerifyAgrees（同区间并发 Verify 结果一致），go test -race 干净。
自检：verify.SelfCheck 经 api.Log.SelfCheck 对外；TestSelfCheck。
