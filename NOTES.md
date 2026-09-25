# DRF 推导（Ccpu=Cmem=60，A(1,6)、B(4,1)；比例相等取 CPU）
① 主导资源：A 比 1/60 与 6/60=1/10 → Mem（交叉乘 1·60<6·60）；B 比 4/60=1/15 与 1/60 → CPU（4·60>1·60）。
② 主导份额：s_A=a_A·6/60=a_A/10；s_B=a_B·4/60=a_B/15。
③ 令 s_A=s_B=s：a_A=10s、a_B=15s；cpu 耗 1·10s+4·15s=70s，mem 耗 6·10s+1·15s=75s。
④ 上界 cpu s≤6/7、mem s≤4/5 → mem 先耗尽，s*=4/5；a_A=8、a_B=12（cpu 56≤60，mem=60 恰达上界）。
甲 平分单位：mem 先耗 → a_A=a_B=60/7≈8.57；份额 A=6/7≈0.857、B=4/7≈0.571；最小 4/7<4/5=0.8，B 被压低约 0.229。
乙 误用 min：两人主导资源都被误判成比例 1/60，仍得 a_A=a_B=60/7；按 max 的真实份额 A=6/7、B=4/7，最小 4/7<0.8。
丙 误判 cpu 绑定：s=6/7 → a_A=60/7≈8.57、a_B=90/7≈12.86；代入 mem=450/7≈64.3，超容量 30/7≈4.29，违反 mem 硬约束。

## 不变量：代码保证位置 / 钉住测试
1 与朴素参照一致：alloc.go Allocate 的 water-filling（零楼层起步、逐相位按楼层定位水位）；TestAllocateMatchesNaiveReference。
2 硬约束且至少一条达上界：alloc.go Allocate 候选 Δr 取 min 的绑定判定（kind=2）与 api.go SelfCheck 复核；TestHardConstraintsAndBinding。
3 主导份额公平：alloc.go 同相位任务取同一水位、floorHeap 按楼层弹出；TestDominantSharesAreFair。
4 失败不留痕：api.go New/Add 全部校验先于写 map 与 Planner.Add；TestRejectedOperationsLeaveNoTrace。
复杂度（非导出 examined，仅同包白盒测试直接读字段）：alloc.go 堆弹出处自增；TestExaminedCountDoesNotScaleWithM。
