# NOTES — ontology-466

初始: epoch=0; B1=10,B2=20,B3=30; V1=30,V2=50,V3=80; 全部 Rev=0（有效，判定为严格 >）。
七步表（值=V1/V2/V3；S=失效视图集合；本步被写的 Rev）:
1. UpdateBase(B2,25)  e=1  值=30/50/80   S={V1,V2,V3}  B2.Rev=1
2. Refresh(V1)        e=1  值=35/50/80   S={V2,V3}     V1.Rev=1
3. Refresh(V2)        e=1  值=35/55/80   S={V3}        V2.Rev=1
4. Refresh(V3)        e=1  值=35/55/90   S={}          V3.Rev=1
5. UpdateBase(B1,100) e=2  值=35/55/90   S={V1,V3}     B1.Rev=2
6. Refresh(V1)        e=2  值=125/55/90  S={V3}        V1.Rev=2
7. Refresh(V3)        e=2  值=125/55/180 S={}          V3.Rev=2
(甲) 第5步后 V3 失效：传递基表 B1.Rev=2 > V3.Rev=1（经 V1 间接）。只传播一层会漏标 V3，误判有效，Value 读到陈旧 90；正确刷新后应为 180。
(乙) 第1步后直接 Refresh(V3) 必须拒绝（直接依赖 V1 失效），状态不变。若不校验而用陈旧 V1=30、V2=50 重算：V3=80、Rev=1 假有效；此后刷新 V1=35/V2=55 仍有 B2.Rev=1<=V3.Rev=1，错值 80 改不回来（正确 90）。
(丙) eager push：V1 先有效即推 V3，读到 V2 旧值 50，V3 暂时错成 35+50=85（正确 90）；V2 有效再推一次。每次推送都像独立 Refresh 记一次 Rev，则 V3.Rev 错成 2（正确做法只刷一次，V3.Rev=1）。

不变量保证位置与钉住的测试:
I1 朴素重算一致：mview.go Refresh 用各直接依赖当前 val 经 expr 重算、Rev 盖当前 epoch；api/api_test.go TestNaiveConsistency（随机序列）与 SelfCheck 钉住。
I2 失效判定正确：dep/dep.go TransitiveBases 给闭包，mview/mview.go staleLocked 对闭包逐基表做严格 Rev> 比较，基表恒有效；api/api_test.go TestStaleness 钉住。
I3 依赖序强制：mview/mview.go Refresh 先遍历 DirectDeps 全部有效才写值；api/api_test.go TestDependencyOrder 钉住。
I4 失败不留痕：mview/mview.go 全部校验先于任何状态变更，四个哨兵互不相同；api/api_test.go TestErrorsDistinctAndAtomic 钉住；并发 api/api_test.go TestConcurrentReaders；传播计数 mview/mview_test.go TestPropagationVisitCount。
