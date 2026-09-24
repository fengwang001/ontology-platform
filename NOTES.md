# NOTES：行级过滤视图增量维护（P: lo<=Val<hi，lo=10,hi=20，视图记 ID:Val）
## 八步分步表
|#|P(before)|P(after)|情形|本步输出|步后视图|
|1 Ins(x,5)|—|假|after不满足|无|{}|
|2 Ins(y,12)|—|真|after满足|+(y,12)|{y:12}|
|3 Upd x5→15|假|真|仅after成立|+(x,15)|{x:15,y:12}|
|4 Upd y12→12|真|真|两侧成立·值不变|无|{x:15,y:12}|
|5 Upd y12→20|真|假|仅before成立|-(y,12)|{x:15}|
|6 Upd y20→3|假|假|两侧都不成立|无|{x:15}|
|7 Upd x15→10|真|真|两侧成立·值变|-(x,15) +(x,10)|{x:10}|
|8 Del(y,3)|假|—|before不满足|无|{x:10}|
甲（Update无条件-前+后）：3:-(x,5)+(x,15) 4:-(y,12)+(y,12) 5:-(y,12)+(y,20) 6:-(y,20)+(y,3)；首违不变量2在第3步-(x,5)（x从不在视图，撤回不存在的行）；最终下游错成{x:10,y:3}（y:3幽灵；正确{x:10}）。
乙（只看after）：第5步漏掉撤回-(y,12)（P(y,20)=假即什么都不输出）；最终下游错成{x:10,y:12}（y:12幽灵）。
丙（值不变也输出-+）：第4步输出-(y,12)+(y,12)，违反不变量3（净零对），最终视图仍{x:10}不变。右端误为闭区间<=20：第5步-(y,12)+(y,20)，第6步-(y,20)，最终视图仍{x:10}相同（临时纳入又被撤回）。

## 四条不变量：代码保证位置 / 钉住测试
1 与批量重算一致：fview.go Apply 用 fpred.Classify 同一规则驱动 src 副本与 cur 覆盖层，提交后 cur 即重算结果；TestViewEqualsBatchRecompute
2 日志前缀自洽：fview.go simOne 对 cur 只按主键直查（locate 计 1）：- 必须与现值 Val 相等、+ 必须该 ID 缺席；TestChangelogPrefixesConsistent
3 最小性：fpred.go Classify 分五态，BothSame/Neither 产出 nil，fview.go Apply 原样返回不补日志；TestMinimalityNoOutput
4 失败不留痕：fview.go Apply 全部变更先在 src 副本+overlay 模拟，全部成功才在 Lock 内提交，任一被拒直接返回不动 src/cur/log；TestRejectedBatchLeavesNoTrace
