# ontology-329 推导与不变量（memLimit=4,maxSpill=2；行格式：步后M｜溢写｜块号｜各未结束事务内存｜输出）
1 M=1 无 {} 1:[a1] 出:无
2 M=2 无 {} 1:[a1] 2:[b1] 出:无
3 M=3 无 {} 1:[a1] 2:[b1,b2] 出:无
4 M=4 无 {} 1:[a1,a2] 2:[b1,b2] 出:无（M==limit 不溢写）
5 M=3 溢写1:[a1,a2]→#0 {#0} 1:[] 2:[b1,b2] 3:[c1] 出:无
6 M=4 无 {#0} 1:[a3] 2:[b1,b2] 3:[c1] 出:无
7 M=3 溢写2:[b1,b2]→#1 {#0,#1} 1:[a3] 2:[] 3:[c1,c2] 出:无
8 M=3 回滚2删#1 {#0} 1:[a3] 3:[c1,c2] 出:无
9 M=4 无 {#0} 1:[a3,a4] 3:[c1,c2] 出:无
10 M=2 溢写1:[a3,a4,a5]→#2 {#0,#2} 1:[] 3:[c1,c2] 出:无
11 M=3 无 {#0,#2} 1:[a6] 3:[c1,c2] 出:无
12 M=2 提交1删#0,#2 {} 3:[c1,c2] 出:a1,a2,a3,a4,a5,a6
(甲) 正确:a1,a2,a3,a4,a5,a6；先内存后块错成:a6,a1,a2,a3,a4,a5；块号降序错成:a3,a4,a5,a1,a2,a6。
(乙) 自第8步起块号不同(误留#0,#1)；第10、11步均因满2块被拒；第12步输出错成:a1,a2,a3,a4（a5,a6未被接受，#1永久泄漏）。
(丙) 用M>=limit：首次溢写在第4步，溢写1:[a1,a2]→#0；第11步被拒（已满2块）；第12步输出错成:a1,a2,a3,a4,a5。
## 不变量保证位置与钉住测试
I1 朴素一致：rbuf.go Append 预查容量+至多一次溢写、end(tx,true) 按块号升序回放；测试 TestNaiveEquivalence。
I2 行序连续：rbuf.go end(tx,true) 先按块号升序读回溢写块再追加内存行；测试 TestCommitReplayOrder、TestTwelveSteps。
I3 无残留：spill.go DeleteTx 在 end 中删除该事务全部块，Append 溢写后 M<=limit；测试 TestNoResidue。
I4 失败不留痕：api.go New 参数校验、rbuf.go Append 容量满在改动前拒绝、Begin/end 先判定存在性；测试 TestRejectedLeavesState。
