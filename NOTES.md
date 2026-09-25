# NOTES

memLimit=3；缓冲容量 3，第 4 条到达时把已装满的 3 条整体成块（FIFO），新条目留在清空后的缓冲。
|#|变更|追加后缓冲|溢写块|
|1|Set(a,1)|[a=1]|无|
|2|Set(b,2)|[a=1,b=2]|无|
|3|Set(c,3)|[a=1,b=2,c=3]|无|
|4|Del(b)|[Del b]|块1=[a=1,b=2,c=3]|
|5|Set(d,4)|[Del b,d=4]|无|
|6|Set(a,9)|[Del b,d=4,a=9]|无|
|7|Del(c)|[Del c]|块2=[Del b,d=4,a=9]|
|8|Set(e,5)|[Del c,e=5]|无|
正确回放：块1→块2→尾缓冲，得 a=9,d=4,e=5；b、c 不存在。
(甲) LIFO（尾→块2→块1）：a 错成 1、b 错成 2、c 错成 3 残留。
(乙) 溢写丢 Del：b 错误残留=2（c 的 Del 在尾缓冲仍生效，c 被正常删除）。
(丙) Del 当 Set(key,"")：b、c 均错成空串且存在；Get(b)=("",true)，存在位错成 true。

不变量（保证位置 / 钉住的测试）：
1 与朴素参照一致：replay/replay.go 的 Replay 按块 FIFO→尾缓冲逐条写 map；TestNaiveReference
2 溢写不丢变更（含 Del）：buf.Append 满额整块复制、Store.Spill 再复制，Replay 的 Del 走 delete；TestNoLoss
3 顺序 FIFO：Store.blocks 只 append，Replay 从旧块到新块再到尾缓冲；TestFIFOOrder
4 失败不留痕：api.Mutate 先校验 committed/key/op 后 Append，New 拒非正 limit；TestRejectedLeavesNoTrace
附：TestConcurrentReaders 钉并发只读逐字段一致；replay 包 TestProbeBound 钉哈希定位不随 m 增长。
