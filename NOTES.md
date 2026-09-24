# 物化视图 per-key 节流器：推导与不变量（W=3）

八步表（批次=条数/值/刷新时刻；视图只在刷新时变）：
1. R(k,0,a): k=1/a/3  m=无       输出=无                  视图={}
2. R(k,2,b): k=2/b/5  m=无       输出=无                  视图={}
3. R(m,2,x): k=2/b/5  m=1/x/5    输出=无                  视图={}
4. Tick(4):  k=2/b/5  m=1/x/5    输出=无（时刻 5>4）       视图={}
5. R(k,5,c): k=3/c/8  m=1/x/5    输出=无                  视图={}
6. Tick(5):  k=3/c/8  m=无       输出=(m,1,x)             视图={m:x}
7. R(m,5,y): k=3/c/8  m=1/y/8    输出=无                  视图={m:x}
8. Stop(8):  无        无         输出=(k,3,c),(m,1,y)     视图={k:c,m:y}

(甲) 并入旧批次。是否刷新只由 Tick 判定，Record 只看该 key 有无「尚未刷新」批次；k 批在 Tick(4) 未到期（5>4）仍挂着，故 t=5 与其刷新时刻并列仍并入，成 3/c/8。若 Tick 错写成「时刻 < now 才刷」：第 6 步 m 应刷成 x，错成仍不可见（批次未结束）；Stop(8) 时 m 本应共 2 批、每批 1 条（x、y 各自一批），错成 1 批 2 条、值 y（(5,x) 与 (5,y) 被错误合并）。
(乙) 合并若不顺延：第 2 步后 k 刷新时刻仍为 3；Tick(4) 中 3<=4，k 本应不可见，却被提前错刷成 b（a、b 两条被算成一批）；最终值 c 虽对，可见时点与批数全错。
(丙) m 两条相邻（先 (2,x) 后 (5,y)，时间单调，间隔恰=W；若字面把 t=5 放到 t=2 之前会被时钟单调规则拒绝，零变更，故取此序）：(5,y) 并入首批，m=2/y/8；Tick(5) 一批都不刷；Stop(8) 得 (k,3,c)、(m,2/y)。最终视图同为 {k:c,m:y}，但 m 由「2 批各 1 条、全局共 3 批」变为「1 批 2 条、全局共 2 批」。差异来自「Record 只并入尚未刷新批次 + Tick（含等号）结束批次」，与间隔是否=W 无关。故不变量 1 只钉 Stop 后：节流本就令窗口内最新值不可见（主序第 5 步 c、第 7 步 y 已接受而视图仍旧），钉中间时刻会与节流矛盾；Stop 强制全部刷出后才与朴素参照等价。

不变量 / 代码保证位置 / 钉住测试：
1. 最终值等价：deb.Batch.Merge 后到者覆盖值、thr.Stop→flush 无视时刻全部刷出，视图仅经 thr.fire 写入 → TestInvariants_FinalAndThrottle（另见 TestEightSteps 全序列）
2. 节流：fired 每批仅 thr.fire 中 +1，视图值只取自已接受批次 deb.Batch.Val → TestInvariants_FinalAndThrottle
3. 时钟单调：thr.Record/flush 入口判 `ts < last` 即拒且先于一切写入，被拒不更新 last → TestInvariant_Monotonic
4. 失败不留痕：thr.New 与 thr.Record 全部哨兵校验都在 map/堆写入之前 → TestRejectionLeavesNoTrace
有序定位复杂度：thr.probes 仅在检查堆顶时 +1（非导出，白箱测试直读字段）→ TestProbeCountConstant
