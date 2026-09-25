# NOTES：变更日志校验和验证

## 第三节推导（segSize=3，e(Seq,Val)=Seq*100+Val）

六条记录的 e：e1=110, e2=220, e3=330, e4=440, e5=550, e6=660。段1=Seq1..3，段2=Seq4..6。

| 步 | 操作 | seg1 | seg2 | total |
|---|---|---|---|---|
| 1 | Append(1,10) | 110 | - | 110 |
| 2 | Append(2,20) | 330 | - | 330 |
| 3 | Append(3,30) | 660 | - | 660 |
| 4 | Append(4,40) | 660 | 440 | 1100 |
| 5 | Append(5,50) | 660 | 990 | 1650 |
| 6 | Append(6,60) | 660 | 1650 | 2310 |
| 7 | 损坏 4:40→70、5:50→80 后 Recompute(1,6) | 660 | 1710 | 2370 |
| 8 | Verify(1,6) 定位 | - | - | 第一条损坏 Seq=4 |

(甲) 只损坏 Seq=5（50→80）：正确的闭区间 Verify(1,5) 返回 corrupt=5, ok=false；
左闭右开 [1,5) 只查 1..4，漏掉 Seq=5，误报 ok=true（无损坏）。
(乙) e 错写成 Seq+Val：总量错成 11+22+33+44+55+66=231（正确 2310）；
第 2 段错成 44+55+66=165（正确 1650）。
(丙) 两条损坏时第一条（升序）是 Seq=4；若按降序扫描返回「第一条」会错报成 Seq=5。

## 四条不变量的保证位置与钉住测试

1. 与朴素参照一致：verify.Log.Verify 按 Seq 升序逐条重算比对（verify.go），测试 TestVerifyMatchesNaive。
2. 总量自洽：Append 中 total+=e 且 segSum 同步累加、Recompute 后 total=ΣsegSum（verify.go），测试 TestTotalConsistent。
3. 定位正确：Verify 返回区间内首个不匹配序号、无损坏 ok=true（verify.go），测试 TestFirstCorruptMinSeq。
4. 失败不留痕：New/Append/Verify/Recompute 先校验后改动，非法输入直接返回哨兵错误（verify.go），测试 TestFailureLeavesNoTrace。
