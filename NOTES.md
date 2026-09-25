# NOTES — 内存 2-3 树

七步表（空树升序插入；代价=本次分裂次数；root/in 为插入后状态）：
1: k=1 cost=0 root=[1] in=[1]
2: k=2 cost=0 root=[1,2] in=[1,2]
3: k=3 cost=1 root=[2] in=[1,2,3]
4: k=4 cost=0 root=[2] in=[1,2,3,4]
5: k=5 cost=1 root=[2,4] in=[1,2,3,4,5]
6: k=6 cost=0 root=[2,4] in=[1,2,3,4,5,6]
7: k=7 cost=2 root=[4] in=[1,2,3,4,5,6,7]

(甲) 根是 [4]：第7步叶 [5,6,7] 分裂、中键6上提，根变 [2,4,6] 后自身再分裂、中键4上提为新根（左内点[2]、右内点[6]）。若不向上传播，根停在3键 [2,4,6]（带4个子节点），违反不变量1：根键数必须∈[0,2]。
(乙) 第7步代价=2（叶分裂+根级联分裂各1）；七次总分裂=0+0+1+0+1+0+2=4。若只统计叶层分裂，第7步错成1、总数错成3。
(丙) Delete(3)：叶[3]删后0键，唯一兄弟[1]仅1键不能借→合并（父键2下移）成叶[1,2]；内点[2]随之0键，其兄弟内点[6]也仅1键→再合并（父键4下移，keys=[4,6]，kids=[叶[1,2]],[叶5],[叶7]]），空根被该合并点取代。共2次合并、0次借位；删除后根=[4,6]，叶子为 [1,2]、[5]、[7]。若只借不合并，会残留一个0键非根叶节点，违反不变量1；且键2只悬在内层分隔符位置、Search(2) 落到空叶返回 false，违反不变量3。

不变量在代码中的保证位置与钉住它的测试：
1. 结构合法：internal/bnode/node.go 的 Node.validate 递归校验键数区间、内部 kids==keys+1、叶同深、键界有序；Node.Validate 从根调用；测试 TestStructuralInvariants（随机树另由 TestRandomOperations 每步调用 btree.Tree.Valid 复核）。
2. 有序性/集合一致：Node.Inorder 产出升序全量键；btree.Tree.Valid 断言其长度==size，api.checkLive 再断言严格升序；测试 TestRandomOperations 以 map 参照逐键核对。
3. Search==二分：api.checkLive 对全量键与缺失键逐一比较 Search 与 sort.SearchInts；测试 TestSearchMatchesBinary 用 slices.BinarySearch 在 7/100/1000 三档核对。
4. 失败不留痕：Insert 先 containsLocked 判重、再判容量；insertRec/deleteRec 在确认命中前不做任何改动；非导出 visited 仅成功时 Store。测试 TestRejectionLeavesNoTrace（三类错误互异、结构与计数器不变、之后可继续用）。
另：visited 为 btree 非导出 atomic 字段，白盒 TestVisitedBound 直接读字段并断言 visited≤height≤40（m=100..10000 五档）；并发由 TestConcurrentInsertsAndSearches 钉住（32 goroutine 互不相交插入 + 并发查找）。
