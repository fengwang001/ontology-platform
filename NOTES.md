# SSI 推导与不变量
初始(版本0): x=10 y=10 z=5；RS/WS=读/写集合，f=in,out；状态只在变化时写出。
01 T1.Begin(sv=0)            T1:RS{} WS{}
02 T2.Begin(sv=0)            T2:RS{} WS{}
03 T1.Read x→10              T1:RS{x10}
04 T2.Read x→10              T2:RS{x10}
05 T1.Read y→10              T1:RS{x10 y10}
06 T2.Read y→10              T2:RS{x10 y10}
07 T3.Begin(sv=0)            T3:RS{} WS{}
08 T3.Write z=99             T3:WS{z99}
09 T3.Read z→99(读己之写,不入RS) T3:RS{}
10 T1.Write x=-10            T1:WS{x-10}
11 T1.Commit 无已提交U f=00 →v1   状态 x=-10 y=10 z=5
12 T3.Commit 对U=T1两交集皆空 f=00→v2 状态 x=-10 y=10 z=99
13 T2.Write y=-10            T2:WS{y-10}
14 T2.Commit U=T1: U.RS∩T2.WS={x,y}∩{y}≠∅→in; U.WS∩T2.RS={x}∩{x,y}≠∅且v1>sv0→out → 回滚,集合丢弃 状态不变
终态 x=-10 y=10 z=99。
(甲) 无rw检测: T1/T2皆提交 → x=-10,y=-10；x+y=-20<0 违反外部约束 x+y≥0。
(乙) 漏读己之写: 第9步读到快照旧值 z=5(正确99)。
(丙) T2先提交: 无U可检→T2提交(y=-10)；T1后提交检出 in+out→T1回滚；终态 x=10,y=-10。rw边只在提交时对已提交U发现，先提交者看不到边，被回滚的总是后提交者；故其等价串行序即提交序，不变量1必须按提交序重放。
不变量(代码位置 / 钉住测试):
1 串行一致: kv.Store.Apply 按递增版本追加 Record 并应用 WS / TestSerialReference
2 写偏回滚: ssi.Txn.Commit 的 in&&out 双标志判定 / TestFourteenSteps
3 读己之写: ssi.Txn.Read 命中本地 WS 先于快照且不入 RS / TestReadYourWrites
4 失败不留痕: ssi.Txn 状态校验+三类哨兵错误,回滚丢弃全部集合 / TestRejectedOpsLeaveNoTrace
计数器 ssi.Manager.examined 非导出,由同包内部测试 TestComplexityExaminedCount 钉住。
