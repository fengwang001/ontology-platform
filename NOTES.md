# ontology-274 推导（delay=3, maxBuffered=10；记号 ID:TS#Seq，缓冲按 (TS,Seq) 列）
|步|事件|前wm|判定|后wm|主输出|旁路|缓冲|
|---|---|---|---|---|---|---|---|
|1|a:5|-∞|缓冲|2|-|-|a5#0|
|2|b:3|2|缓冲(3>2)|2|-|-|b3#1,a5#0|
|3|c:9|2|缓冲|6|b3#1,a5#0|-|c9#2|
|4|d:5|6|迟到(5<=6)|6|-|d5#3|c9#2|
|5|e:6|6|迟到(6<=6)|6|-|e6#4|c9#2|
|6|f:9|6|缓冲|6|-|-|c9#2,f9#5|
|7|g:4|6|迟到|6|-|g4#6|c9#2,f9#5|
|8|h:12|6|缓冲|9|c9#2,f9#5|-|h12#7|
|9|i:12|9|缓冲(12>9)|9|-|-|h12#7,i12#8|
|10|j:16|9|缓冲|13|h12#7,i12#8|-|j16#9|

Flush()：wm=+∞，释放 j16#9。最终主输出 b3,a5,c9,f9,h12,i12,j16；旁路 d5,e6,g4（到达序）。

(甲) 迟到判据写成 TS<wm：e:6 不再迟到而进缓冲，第 8 步 h 抬 wm=9 时与 c,f 同批释放，主输出成 e6#4,c9#2,f9#5（按 (TS,Seq)），e 在第 8 步进主输出。释放条件写成 TS<wm：第 8 步 9<9 不成立、主输出为空，c、f 推迟到第 10 步与 h12、i12 一起释放（j 仍由 Flush 释放）。

(乙) 正确顺序：第 8 步 c9#2→f9#5，第 10 步 h12#7→i12#8（同 TS 由 Seq 破平手）。仅按 TS 且后到先出（栈）：第 8 步错成 f9#5→c9#2，第 10 步错成 i12#8→h12#7；违反不变量 2（主输出 (TS,Seq) 不再严格递增），也违反不变量 1（与朴素参照的稳定排序逐条一致）。

(丙) 迟到事件直接追加进主输出：b3,a5,d5,e6,g4,c9,f9,h12,i12,j16；首次 TS 下降在第 7 步：e:6 之后接 g:4（6→4）。直接丢弃则丢 d:5、e:6、g:4 三个。

## 不变量落点与钉住的测试

1. 朴素参照一致：旁路在 api.go 的 Push 迟到分支按到达序立即追加；主输出由 rbuf.go 的 Release 经 (TS,Seq) 最小堆弹出。测试 TestReferenceEquivalence（checkAll 内嵌朴素参照逐条比对）。
2. 主输出严格有序：rbuf.go 堆 Less 用 order.Less，Release 只弹 TS<=wm；api.go 仅原样转交。测试 TestMainOutputOrder（含「输出时 TS<=当时 wm」逐批断言）。
3. 恰好一次：rbuf.go Release 弹出即从堆删除，api.go Flush 以 +∞ 排空，迟到者只入 side。测试 TestReferenceEquivalence 与 TestConcurrentPush（checkAll 断言总数与去重数）。
4. 失败不留痕：api.go Push 在任何状态修改前依次判空 ID/重复 ID/超限，非法参数在 New 拒绝。测试 TestRejectionNoTrace（含 Seq 不被消耗、水位线不变、四哨兵互异）。

复杂度证明：rbuf.lastProbes 为非导出字段，包内测试 TestReleaseProbeBound 直接读取并断言 =k+1（m 取 100..10000 多档，k=0,3）。并发：TestConcurrentPush（race）。SelfCheck 由 demo 调用。
