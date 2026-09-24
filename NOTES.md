# NOTES — order-statistic quantiles

maxValues=8 的八步表（中位数 / p90）：
1. Insert(10): [10], n=1, 中位 10, p90 10
2. Insert(30): [10 30], n=2, 中位 20, p90 30
3. Insert(20): [10 20 30], n=3, 中位 20, p90 30
4. Insert(40): [10 20 30 40], n=4, 中位 25, p90 40
5. Insert(10): [10 10 20 30 40], n=5, 中位 20, p90 40
6. Delete(30): [10 10 20 40], n=4, 中位 15, p90 40
7. Insert(50): [10 10 20 40 50], n=5, 中位 20, p90 50
8. Delete(10): [10 20 40 50], n=4, 中位 30, p90 50
(甲) 第6步正确中位数 (10+20)/2=15；只取第 n/2+1 小会错成 20。
(乙) 第7步 rank=ceil(4.5)=5 → 50；floor 得 rank=4 → 错成 40。
(丙) 第8步只删一次：n=4、[10 20 40 50]、中位 30。若去重且 Delete 删光：第5步 Insert(10) 为空操作，第8步后 n=3、[20 40 50]、中位错成 40。朴素切片必须含重复——秩按多重集位次定义，去重改变 n 与各秩位置（第5步起即不同），批量参照就不再是规则定义的同一序列。

不变量（保证位置 / 钉住的测试）：
1. 与批量重算一致：qnt.Engine.kth 的秩公式 + ost Kth 按子树大小定位 / TestNaiveBatchConsistency
2. 中位≤p90：p90 秩恒不小于中位秩，Kth 序列单调不降 / TestMedianNotAboveP90
3. 秩与计数自洽：ost 每个节点 subSize 经 pull/balance 维护，Count=subSize(root) / TestKthSequenceAndCount
4. 失败不留痕：api.Insert 先查容量再写入、ost.erase 未命中原样返回节点、四类哨兵错误 / TestRejectedOpsLeaveState
