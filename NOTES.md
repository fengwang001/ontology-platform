# NOTES

## 推导 W=2，事件序：1,2,3,4,6,9,2,3,6,10,11
|步|事件|H|seen|gaps|备注|
|1|1|1|{}|{}|并入|
|2|2|2|{}|{}|并入|
|3|3|3|{}|{}|并入|
|4|4|4|{}|{}|并入|
|5|6|4|{6}|{}|在途：6-(H+1)=1<W，5 未错过窗口，不判洞|
|6|9|7|{9}|{5,7}|判洞5→并入6→判洞7；8 距窗不足，停|
|7|2|7|{9}|{5,7}|seq<=H，忽略，三态不变|
|8|3|7|{9}|{5,7}|忽略|
|9|6|7|{9}|{5,7}|忽略|
|10|10|10|{}|{5,7,8}|判洞8→并入9→并入10|
|11|11|11|{}|{5,7,8}|并入|
- (甲) 第5步不判洞，Gaps()={}；「seq>H+1 即把 H+1..seq-1 全判洞」会误报 **5**。
- (乙) gaps={5,7}，洞大小 **2**；把在途的 6、未错过窗的 8 也算洞 → {5,6,7,8}，洞大小错报成 **4**。
- (丙) H=7、gaps={5,7} 三步全不变；定容 W 环形缓冲不去重，2,3 占满后 6 挤出真正在途的 **9**，9 被误判为洞。

## 不变量落点（代码位置 / 钉住的测试）
1. 三分划分与朴素参照逐序号一致：`det.(*Detector).Feed` 收敛循环（seen 删/H++/gaps 追加，map+maxSeen）；钉于 `TestNaiveReference`。
2. 无早判：仅当 `seq.MissedWindow(H+1,maxSeen,W)`（maxSeen-(H+1)>=W）才判洞；钉于 `TestWindowNoEarlyGap`。
3. 重复不改态、洞只增不减不重复：`Feed` 起首 `seq.Covered` 命中即 return，gaps 只按 H 升序 append；钉于 `TestDuplicateIgnored`。
4. 失败不留痕：`api.New` 构造前拒 W<=0；`det.Feed` 改态前先过 `seq.Valid` 与 MaxInt64-W 溢出判定；钉于 `TestRejectionNoTrace`。
- O(1)：非导出 `det.checks` 计收敛检查数，`TestConstantWork` 断言 m∈{100,1000,10000} 喂 m+1 时 checks<=3；并发：`det.mu` 互斥，`TestConcurrentFeed`。
