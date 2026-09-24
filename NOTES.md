# NOTES
题：src=[a,b,a,c,b,a]，chunk=2；p=processed，sh=shadow，cp=检查点，com=committed，g=gen。
1 New    : p=0 sh={}          cp={0,{}}            com={} g=0
2 Start  : p=0 sh={}          cp={0,{}}            com={} g=0
3 Step   : p=2 sh={a1,b1}     cp={2,{a1,b1}}       com={} g=0
4 Step   : p=4 sh={a2,b1,c1}  cp={4,同sh}          com={} g=0
5 Crash  : p=4 sh=丢弃        cp={4,{a2,b1,c1}}    com={} g=0
6 View   : p=4 sh=-           cp={4,{a2,b1,c1}}    com={} g=0 读={}
7 Start  : p=4 sh={a2,b1,c1}  cp={4,同sh}          com={} g=0
8 Step   : p=6 sh={a3,b2,c1}  cp={6,同sh}          com={} g=0
9 Commit : p=6 sh={a3,b2,c1}  cp={6,同sh}          com={a3,b2,c1} g=1
(甲) 第6步 View 必须返回旧视图 {}；原地重建会错返半成品 {a:2,b:1,c:1}。
(乙) 归零但留 sh 再全量重放：整段 src 被叠加两次 → {a:4,b:3,c:2}；正确 {a:3,b:2,c:1}。
(丙) 返回 ErrIncomplete，com 仍 {}、g 仍 0；不检查完成度者错切 {a:2,b:1,c:1}、g=1。
不变量（代码保证位置 / 钉住的测试函数）：
1 朴素一致：reb.Step 用 agg.Add 按序计入、Commit 拷贝切换（reb/reb.go）；TestNaiveReplayConsistency
2 旧视图可读：View/Gen 只读 committed/gen，shadow 永不可见（api/api.go）；TestOldViewReadable
3 续跑不重不丢：Start 从 cp 恢复 p/sh，Crash 不动 cp（reb/reb.go）；TestCrashResume、TestAppliedCounterBounded
4 失败不留痕：各方法先判错、后改状态（reb/reb.go）；TestRejectedOpsLeaveNoTrace
