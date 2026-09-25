# NOTES

推导 N=4 M=2, B=[1,5,2,6,9,3], P=[5,9,3,2,1,7], h(k)=k mod 4。

分区结构：p0=[]；p1=[1,5,9]（3 条 > M=2，溢出）；p2=[2,6]（2 条，不溢出）；p3=[3]（1 条，不溢出）。

| 探针键 | 落入分区 | 是否溢出 | 匹配到的 build 键 | 本步输出 |
|---|---|---|---|---|
| 5 | 1 | 是 | 5 | (5,5) |
| 9 | 1 | 是 | 9 | (9,9) |
| 3 | 3 | 否 | 3 | (3,3) |
| 2 | 2 | 否 | 2 | (2,2) |
| 1 | 1 | 是 | 1 | (1,1) |
| 7 | 3 | 否 | 无 | 无 |

正确结果共 5 条：(5,5)(9,9)(3,3)(2,2)(1,1)。

(甲) 探测误用 h'(k)=(k+1) mod 4：5→p2[2,6] 不中、9→p2 不中、3→p0 空、2→p3[3] 不中、1→p2 不中、7→p0 空，整表输出 **0 条**。

(乙) p1 只保留前 M=2 条 [1,5]、丢弃 9 且不写溢写区：p=9 无匹配，**漏掉 Key 9**，整表从 5 条错成 **4 条**。

(丙) 溢出分区探测时整体跳过、不重扫溢写区：第 1、2、5 步全丢，**漏掉 Key 5、9、1**，只剩 (3,3)(2,2)，整表错成 **2 条**。

不变量在代码中的保证位置与钉住的测试：

1. 与朴素参照一致：hjoin/hjoin.go 的 Probe 按 P 顺序逐条、按 B 建表顺序展开计数乘积；由 hjoin/hjoin_test.go 的 TestNaiveEquivalence（多档随机输入）钉住。
2. 分区一致性：part/part.go 的 H 是唯一分区函数，hjoin 建表与探测都调它；由 hjoin/hjoin_test.go 的 TestPartitionConsistency 钉住。
3. 溢出不影响结果：hjoin/hjoin.go 的 Build 在分区溢出时把全部元组写入 spill map，Probe 对溢出分区重扫 spill 与 mem；由 hjoin/hjoin_test.go 的 TestSpillDoesNotChangeResult 钉住。
4. 失败不留痕：api/api.go 的 New/Build/Probe 先全量校验、通过后才整体提交；由 api/api_test.go 的 TestRejectedOpsLeaveNoTrace 钉住。
