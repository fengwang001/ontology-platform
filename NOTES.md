# ontology-263 NOTES（N=3, maxRows=16；排序：Score 降序，平分 Key 字节序升序）
步 | 变更日志 | 本步后 Top-N | 榜外存活
1 | +(a,50) | [a50] | —
2 | +(b,70) | [b70,a50] | —
3 | +(c,50) | [b70,a50,c50] | —
4 | −(c,50) +(d,60) | [b70,d60,a50] | [c50]
5 | 无 | [b70,d60,a50] | [c50,e50]
6 | −(b,70) +(c,50) | [d60,a50,c50] | [e50]
7 | 无 | [d60,a50,c50] | —
8 | −(c,50) +(f,55) | [d60,f55,a50] | [c50]
9 | −(d,60) +(c,50) | [f55,a50,c50] | —
10 | 无 | [f55,a50,c50] | [g45]
甲：第6步由 c50 补位——榜外 c50 与 e50 同分，Key 字节序 c<e，c 排序更靠前。只保留 N 个候选行的实现：c50 在第4步被 d60 挤出榜时即丢弃；第6步只能输出 −(b,70)，Top-N 退化为 [d60,a50]，缺一行。
乙：平分改 Key 降序：首次不同在第4步，输出 −(a,50) +(d,60)（正确是 −(c,50)）；第5步输出 −(c,50) +(e,50)（正确无）；第7步输出 −(e,50) +(a,50)（正确无）。
丙：撤回不补位：第6步仅 −(b,70)（榜=[d60,a50]）；第9步仅 −(d,60)（榜=[f55,a50]）；第10步由新增行填坑 +(g,45)，下游持有 [f55,a50,g45]。正确批量结果为 [f55,a50,c50]：第6步起下游行数 2≠min(3,存活3) 违反不变量2，第10步后又违反不变量1（g45 顶替了榜外的 c50）。

不变量保证位置 / 钉住测试：
I1 与批量重算一致：topn/topn.go applyOne 变更后由 topRows 对 rank.Ordered 取前 min(n,len)，diffTop 做前后集合差；TestTenSteps、TestBatchConsistencyRandom。
I2 变更日志前缀自洽：topn/topn.go diffTop 先 − 后 +，api/api.go replay/SelfCheck 回放每个前缀校验持有行；TestChangelogPrefix。
I3 严格有序无重复 Key：rank/rank.go Insert 内 sort.Search 二分定位插入点、idx map 唯一去重；rank TestOrdering 与 api TestTenSteps。
I4 失败不留痕：topn/topn.go Apply 在 Clone 副本上试跑、遇哨兵错误整体丢弃（存活行/榜/日志均不变）；TestRejectNoTrace、TestSentinelErrors。
