# NOTES
推导记法：均为 Key=k；活行简写 ID+T（如 p3 = ID=p,T=3）；输出 +(ID,T)/-(ID,T)。
## 九步分步表
|步|存活行(升序)|本步输出|视图 k|
|1|a5|+(a,5)|a5|
|2|a5,b8|无|a5|
|3|p3,a5,b8|-(a,5) +(p,3)|p3|
|4|m3,p3,a5,b8|-(p,3) +(m,3)|m3|
|5|p3,a5,b8|-(m,3) +(p,3)|p3|
|6|e1,p3,a5,b8|-(p,3) +(e,1)|e1|
|7|e1,p3,a5|无|e1|
|8|p3,a5|-(e,1) +(p,3)|p3|
|9|a5|-(p,3) +(a,5)|a5|
## (甲)(乙)(丙)
(甲) 第5步只输出 -(m,3)，视图错为「无 k」（正确：再 +(p,3)，视图 p3）。
(乙) 第6步输出「无」，e 后到不顶首条；视图错为 a5（正确 e1）。
(丙) T 并列按到达先后：第4步输出「无」、视图 p3（正确 m3）；第5步起两实现视图重合；日志条目总数：正确 13，并列先到 9。
## 四条不变量：代码位置 / 钉住的测试
1 与批量重算一致：每 Key 一棵 rank.Set，api.View 取各 Min（api.go）/ TestViewNaive、TestNineSteps。
2 日志前缀自洽：dedup.step 仅在 old≠new 时按 -old、+new 追加（dedup.go）/ TestLogPrefixes。
3 输出最小：每步产出 0/1/2 条，首条相同即不产出（dedup.go Apply）/ TestOutputMinimal。
4 失败不留痕：Apply 出错逆序回滚存活行、日志仅整批成功时提交（dedup.go）/ TestRejectedBatchAtomic。
