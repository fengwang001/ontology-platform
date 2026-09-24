# NOTES — GROUPING SETS 增量聚合

八步表（G={(A),(A,B),()}，简写 xp=(x,p)、xq=(x,q)、yp=(y,p)，值写 键→sum）：

| 步 | () sum | 组(A) 条目 | 组(A,B) 条目 |
|---|---|---|---|
| 1 +f1 | 10 | x→10 | xp→10 |
| 2 +f2 | 30 | x→30 | xp→30 |
| 3 +f3 | 35 | x→35 | xp→30, xq→5 |
| 4 +f4 | 42 | x→35, y→7 | xp→30, xq→5, yp→7 |
| 5 −f2 | 22 | x→15, y→7 | xp→10, xq→5, yp→7 |
| 6 +f5 | 25 | x→15, y→10 | xp→10, xq→5, yp→10 |
| 7 −f1 | 15 | x→5, y→10 | xq→5, yp→10（xp 归零移除） |
| 8 +f6 | 17 | x→7, y→10 | xq→7, yp→10 |

(甲) (A,B) ID=0b001=1，() ID=0b111=7；(A,B) 中 GROUPING(C)=1。极性写反（1=参与分组）：(A,B)=0b011=3，()=0b000=0。
(乙) ROLLUP(A,B,C)=(A,B,C),(A,B),(A),()，仅多 (A,B,C)：ID=0、GROUPING=(0,0,0)。CUBE 共 8 组，除已有 3 组外多 (A,B,C)0/(0,0,0)、(A,C)2/(0,1,0)、(B,C)4/(1,0,0)、(B)5/(1,0,1)、(C)6/(1,1,0)。
(丙) 第 7 步后 (x,p) sum=0，必须移除；若零值也物化保留，第 8 步后会多 (x,p)→0，live 条目数由 2 错成 3。

不变量 → 代码保证位置 / 钉住的测试函数：

- I1 与批量重算一致：`agg/agg.go` Apply 按投影累加、归零即 delete；`api/api.go` SelfCheck 内置序列批量重算后逐项比对 → TestEightStepDerivation、TestRandomBatchEquivalence。
- I2 组完整性：`agg/agg.go` New 只登记传入的 gset.Group、View 只输出这些组的 ID → TestGroupCompleteness。
- I3 撤回不越界：`agg/agg.go` Apply 在任何写入前对每组预检 cur+M≥0，不过即拒 → TestRandomBatchEquivalence（逐步断言不存在负值）。
- I4 失败不留痕：`agg/agg.go` Apply 先全部预检通过后才统一提交；`api/api.go` New 先校验完全部分组集再构造 → TestRejectionAtomic。
