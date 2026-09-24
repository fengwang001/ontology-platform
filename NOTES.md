# NOTES — ontology-270 推导与不变量
八步（maxGroups=3，每步一次 Apply；行表用 id:g(v) 简写，只写变化）：
1 | 1:a5 | a(5,1) | +(a,5,1)
2 | 1:a5 2:a-5 | a(0,2) | -(a,5,1) +(a,0,2)
3 | +3:b7 | a(0,2) b(7,1) | +(b,7,1)
4 | 1:a8 | a(3,2) b(7,1) | -(a,0,2) +(a,3,2)
5 | 2:b3 | a(8,1) b(10,2) | -(a,3,2) +(a,8,1) -(b,7,1) +(b,10,2)
6 | 1:c8 | b(10,2) c(8,1) | -(a,8,1) +(c,8,1)
7 | 同6 | b(10,2) c(8,1) | 无
8 | 删2 | b(7,1) c(8,1) | -(b,10,2) +(b,7,1)
(甲) 第5步：-(a,3,2) +(a,8,1) -(b,7,1) +(b,10,2)。若只向新组加入不从旧组撤回，第5步错为 -(b,7,1) +(b,10,2)；八步后视图错为 a(3,2) b(7,1) c(8,1)（幽灵组 a，共3组，正确为2组）。
(乙) 删除条件写成 sum==0：第2步错为只输出 -(a,5,1)（count=2 的组被删、视图空）。count 归零仍保留且出 +(a,0,0)：第6步错为 -(a,8,1) +(a,0,0) +(c,8,1)，八步后视图 3 组（多出 a(0,0)）。
(丙) Update 一律拆成 Delete+Insert 各自输出：第4步错为 -(a,0,2) +(a,-5,1) -(a,-5,1) +(a,3,2)；第7步（值未变）错为 -(b,10,2) +(b,3,1) -(b,3,1) +(b,10,2)。
不变量保证位置 / 钉住测试：
I1 重算一致：gagg.go Apply 循环内按 gdelta.Apply 的净值更新 agg，after.Count<=0 即 delete(agg,g)；View() 用 maps.Clone 返回只含 count>0 组的拷贝。TestViewMatchesRecompute
I2 日志自洽：gdelta.go Entries 同组先 - 后 +；gagg.go Apply 按序收集 out，仅整批成功后才 append 进 log。TestChangeLogPrefixes
I3 最小输出：gdelta.go Deltas 同组 Update 只给一个净增量（DCount=0，值不变 DSum=0），Entries 在 before==after 时返回 nil、同组至多一减一加。TestMinimalOutput
I4 失败不留痕：gagg.go Apply 对每次 map 改动先存 image（im 切片），任一条非法即 rollback 逆序还原 rows/agg，且不 append log。TestRejectedBatchAtomic
读行计数（gagg.go 非导出字段 reads，gdelta 单操作 O(1) 增量）：TestIncrementalReads；并发与竞态：TestConcurrent。
