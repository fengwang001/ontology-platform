# 订阅增量推送器 NOTES

## 第三节：七步分步表（S1=id1、S2=id2，bound 均 "m"）
| 步 | Seq | 步后Delivered | S1 | S2 |
|---|---|---|---|---|
| 1 Push("a") | 1 | 1 | 否："a"<"m" | 未注册 |
| 2 Push("m") | 2 | 2 | 命中，收到 (1,2,"m","") | 未注册 |
| 3 Subscribe(S2,"m") | — | 2 | 在位 | 注册，自位点 2 之后生效 |
| 4 Push("l") | 3 | 3 | 否："l"<"m" | 否 |
| 5 Push("m") | 4 | 4 | 收到 (1,4,"m","") | 收到 (2,4,"m","") |
| 6 Unsubscribe(S1) | — | 4 | 已从活跃集合移除 | 在位 |
| 7 Push("z") | 5 | 5 | 不收（已退订） | 收到 (2,5,"z","") |

（甲）正确实现第 2 步 S1 命中（字典序 >= 含等号）。若误写成 `Key > bound`，S1 第 2 步收不到；S1 推送列表应由 [(1,2,"m"),(1,4,"m")] 错成只剩 [(1,4,"m")]，漏掉 seq=2。
（乙）S2 收不到第 2 步那条 "m"（注册时 Delivered=2，仅判定此后变更）。若注册即补推历史，S2 额外收到 (2,2,"m","")，违反不变量 1（增量语义）。
（丙）第 7 步 S1 不应收到；惰性退订会使 S1 错误收到 (1,5,"z","")，违反不变量 2（退订零推送）。

## 第二节：四条不变量的保证位置与测试
1. 增量语义：eng.go 的 Push 只遍历当前活跃切片 subs，注册之前的变更不可能被投递给尚不存在的订阅；由 TestSevenSteps、TestIncrementalNoBackfill 钉住。
2. 退订零推送：eng.go 的 Unsubscribe 同时从有序切片删除并删 exists 索引，Push 只扫活跃切片；由 TestUnsubscribeZero、TestSevenSteps 钉住。
3. 边界含等号：sub.go 的 Hit 取 key >= bound；eng.go Push 用二分定位首个 bound>key，相等 bound 留在命中前缀；由 TestBoundEquality、TestSevenSteps 钉住。
4. 失败不留痕：eng.go 的 Subscribe/Unsubscribe 在校验全部通过前不做任何写入，错误为互不相同的哨兵 ErrInvalidID/ErrDuplicateID/ErrUnknownID；由 TestRejectedOpsStateUnchanged、TestSentinelErrorsDistinct 钉住。
