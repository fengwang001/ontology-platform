# NOTES

推导：timeout=5，初始 A=W=lastHB=now=0，stale=(A<W)||(now-lastHB>5)。

| # | 操作 | A | W | now | lastHB | now-lastHB | stale |
|---|---|---|---|---|---|---|---|
| 1 | Data{1,10} | 1 | 0 | 0 | 0 | 0 | false |
| 2 | Watermark{4} | 1 | 4 | 0 | 0 | 0 | true |
| 3 | Data{2,20} | 2 | 4 | 0 | 0 | 0 | true |
| 4 | Data{3,30} | 3 | 4 | 0 | 0 | 0 | true |
| 5 | Data{4,40} | 4 | 4 | 0 | 0 | 0 | false |
| 6 | Tick(5) | 4 | 4 | 5 | 0 | 5 | false |
| 7 | Tick(6) | 4 | 4 | 6 | 0 | 6 | true |
| 8 | Heartbeat{} | 4 | 4 | 6 | 6 | 0 | false |

(甲) 第5步正确 stale=false；错写 A<=W：4<=4 成立 → 错判 true。
(乙) 第6步正确 stale=false；错写 now-lastHB>=5：5>=5 成立 → 错判 true。
(丙) 第7步正确 stale=true；只看滞后(A==W 即新鲜) → 错判 false。

不变量（保证位置 / 钉住测试）：
1. 视图=已应用 Val 之和、Applied=最后 Seq：stale.go Feed 的 Data 分支仅在校验通过后 view+=Val；api_test TestViewEqualsAppliedSum。
2. Stale()==(A<W)||(now-lastHB>timeout)：stale.go Detector.Stale 单次三个比较；api_test TestEightStepTable、TestBoundaryBooleans。
3. W/lastHB/now 只增不减：wm.go RaiseWatermark/Beat、stale.go Tick 先检后改；api_test TestMonotonicNonDecreasing（W）、stale_internal_test TestClockMonotonic（now/lastHB）。
4. 拒绝不留痕：各入口失败返回先于任何赋值（wm.go、stale.go Tick/Feed、api.go New）；api_test TestRejectionLeavesNoTrace。
