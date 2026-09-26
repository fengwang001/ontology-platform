# NOTES — Count-Min Sketch（ontology）

推导 w=6,d=3：h1=x%6，h2=(2x+1)%6，h3=(5x+3)%6；按序 Add(2,4)、Add(5,2)、Add(11,1)。

| 步 | 命中 (h1,h2,h3) | 行1 列:值 | 行2 列:值 | 行3 列:值 |
|---|---|---|---|---|
| Add(2,4) | (2,5,1) | 2:4 | 5:4 | 1:4 |
| Add(5,2) | (5,5,4) | 2:4, 5:2 | 5:6 | 1:4, 4:2 |
| Add(11,1) | (5,5,4) | 2:4, 5:3 | 5:7 | 1:4, 4:3 |

（只列受影响的非零列；5 与 11 在三行全部同列；行2列5 另被 key 2 撞。）

(甲) Query(2) 命中 (2,5,1)：min(行1列2=4, 行2列5=7, 行3列1=4)=4；「取 min」误写成「取 max」会错成 7。
(乙) Add 只累加行1：行2、行3 恒为 0，Query(2)=min(4,0,0)=0 < 真实频数 4，直接低估，违反不变量 1。
(丙) Query 误写为 d 行求和：4+7+4=15。Query(5) 命中 (5,5,4)，正确 min(3,7,3)=3（真实 2，被三行全撞列的 11 高估；行2列5=7 还含 key 2 的 4），求和会错成 13。

## 不变量（代码保证位置 / 钉住的测试函数）

1. 绝不低估：sketch.Add 对 d 行逐行累加、sketch.Query 取 d 行 min（sketch/sketch.go）— TestNoUnderestimate。
2. 单键精确：无冲突时每个命中格只含本键计数，min 恰为真实值（sketch.Query）— TestSingleKeyExact。
3. 与精确 map 一致：多档规模+随机 key 顺序下 Query(x)≥c(x)，无冲突集合 Query(x)==c(x)（sketch.SelfCheck 同验）— TestMatchesExactMap。
4. 失败不留痕：api 的 New/Add/Query 先判定哨兵错误、上锁改表在其后（api/api.go）— TestRejectedOpsLeaveState。

查询只碰 d 个格子：非导出字段 sketch.lastQueryProbes（包内测试直接读字段，不经任何导出方法）— TestQueryTouchesExactlyDCells。
并发逐 key 一致：sketch 用 sync.RWMutex 护表，api.Query/SelfCheck 并发只读（sketch/sketch.go）— TestConcurrentQueries。
