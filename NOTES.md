# NOTES
九事件(n=3)逐步表(e,节点,执行后L,recv取max,全序位):
e1 P3 1 — 2
e2 P3 2 — 4
e3 P2 1 — 1
e4 P1 3 max(0,2) 5
e5 P1 4 — 7
e6 P2 2 — 3
e7 P2 5 max(2,4) 9
e8 P3 3 max(2,2) 6
e9 P1 5 — 8
正确全序: e3 e1 e6 e2 e4 e8 e5 e9 e7
(甲)不取max的错误ts: e1=1 e2=2 e3=1 e4=1 e5=2 e6=2 e7=3 e8=3 e9=3
违反时钟条件对(ts(a)>=ts(b)): (e1,e4)1=1 (e2,e4)2>1 (e2,e5)2=2
(乙)按执行先后破并列的错误全序: e1 e3 e2 e6 e4 e8 e5 e7 e9
与正确全序比位置1,2,3,4,8,9不同(5,6,7相同)
(丙)e6与e4互不happens-before(并发);误把全序在前当因果会错判 e6→e4
全序36个有序对中HB对21个,并发对=36-21=15个
不变量保证位置/钉住测试:
1 Order==批量重算: hist/hist.go appendEvent二分定位+有序切片; TestOrderMatchesBatch
2 时钟条件(HB则ts严格小): lc/lc.go Tick与Recv的max规则; TestClockConditionAllPairs
3 全序严格(同节点ts严格递增,(ts,node)唯一): lc.Less+append时钟推进; TestStrictTimestamps
4 失败不留痕(先校验后改状态,同一把互斥锁): hist/hist.go Local/Send/Recv; TestRejectionLeavesNoTrace
