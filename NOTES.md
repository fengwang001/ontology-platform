# NOTES

## 1. period=3，元素 10..80 分步表
| 步 | 元素 | sum | cnt | 触发 |
|---|---|---|---|---|
| 1 | 10 | 10 | 1 | 无 |
| 2 | 20 | 30 | 2 | 无 |
| 3 | 30 | 60 | 3 | 快照 (60,3) |
| 4 | 40 | 100 | 4 | 无 |
| 5 | 50 | 150 | 5 | 无 |
| 6 | 60 | 210 | 6 | 快照 (210,6) |
| 7 | 70 | 280 | 7 | 无 |
| 8 | 80 | 360 | 8 | 无 |

(甲) 正确 (210,6)。若触发即清空：第3步后归零，第6步只累计 40+50+60=150、cnt=3，输出 (150,3)：sum 少 60、cnt 少 3。
(乙) 条件 cnt%3==1 在 cnt=1,4,7（步 1/4/7）触发，首次快照 (10,1)；正确应在步 3、6 触发。
(丙) 正确 (60,3)。判定放在加入前则快照不含触发元素 30，第3步输出 (30,2)。

## 2. 不变量落点
1. 与批量重算一致：gw.Acc.Add 先累加、gw.Triggered 后判 cnt（gw/gw.go），gwin Table.Feed 用运行中 sum/cnt 直接成像；TestBatchRecompute 钉住。
2. 触发不重置：gwin Table.Feed 触发分支只 append 快照，不写 sum/cnt（gwin/gwin.go）；TestNoReset 钉住。
3. 快照单调：快照只在提交阶段按触发顺序 append，读时 RLock 拷贝（gwin/gwin.go）；TestMonotonicSnapshots 钉住。
4. 失败不留痕：gwin Table.Feed 先影子演算校验整批（空 Key/超限）再提交，New 拒 period<=0；TestRejectedBatchAtomic 钉住。
