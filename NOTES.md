# ontology-276 NOTES

## 九步推导（n = n(x),n(y)；视图为当前行集合）
|步|本步输出（按序）|n(x),n(y)|本步之后视图全部行|
|1|无|1,0|∅|
|2|+(l1,r1)|1,0|(l1,r1)|
|3|+(l2,NULL)|1,0|(l1,r1),(l2,NULL)|
|4|+(l3,NULL)|1,0|(l1,r1),(l2,NULL),(l3,NULL)|
|5|-(l2,NULL) +(l2,r2) -(l3,NULL) +(l3,r2)|1,1|(l1,r1),(l2,r2),(l3,r2)|
|6|+(l2,r3) +(l3,r3)|1,2|(l1,r1),(l2,r2),(l2,r3),(l3,r2),(l3,r3)|
|7|-(l2,r2) -(l3,r2)|1,1|(l1,r1),(l2,r3),(l3,r3)|
|8|-(l2,r3) +(l2,NULL) -(l3,r3) +(l3,NULL)|1,0|(l1,r1),(l2,NULL),(l3,NULL)|
|9|-(l1,r1) +(l1,NULL)|0,0|(l1,NULL),(l2,NULL),(l3,NULL)|

(甲) 第6步正确输出仅 +(l2,r3) +(l3,r3)。错版（插入匹配R就撤NULL）输出 -(l2,NULL) +(l2,r3) -(l3,NULL) +(l3,r3)；下游多重集计数后 (l2,NULL) 计数错成 -1（撤回了计数为0的行），违反不变量2。
(乙) 删R从不补NULL：第9步后视图错成 ∅，比正确结果少 (l1,NULL),(l2,NULL),(l3,NULL) 三行。每条匹配R删除都补NULL（不看n(k)）：第7步首次违反不变量3，此刻 (l2,NULL) 与 (l2,r3) 同时存在（l3 同）。
(丙) 插L不查R：第2步错输出 +(l1,NULL)；视图比批量重算多 (l1,NULL)、少 (l1,r1)。

## 四条不变量：保证位置 / 钉住的测试
1 与批量重算一致：api.go 的 View() 由当前 L/R 两表做朴素嵌套循环重算（L ID、R ID 双升序）；TestViewMatchesBatchRecompute。
2 日志前缀自洽（计数仅0/1、-必撤回计数1）：ljoin.go 的 insert/remove 按规则成对产出，api.go Apply 成功才按序追加；TestChangeLogPrefixes。
3 NULL行互斥（每l每批次末恰一种形态）：ljoin.go insert 插R仅在插入前 n(k)=0 撤NULL、remove 删R仅在删除后 n(k)=0 补NULL；TestNullMutex。
4 失败不留痕：ljoin.go Apply 全程在 jstate.Clone() 上处理、成功才换入，api.go Apply 失败不追加日志；TestRejectedBatchLeavesNoTrace。
