# NOTES: ontology-484 物化视图重写（基表8行；输出行序 region,year,amount；投影列序 region,amount,year）
Q1 region=east;{region,amount} | V1 | 无 | (east,50),(east,120),(east,200),(east,90)
Q2 region=east∧amount>=100;{region,amount} | V1 | amount>=100 | (east,120),(east,200)
Q3 amount>100;{region,amount} | V2 | amount>100 | (east,120),(east,200),(west,150),(west,300)
Q4 region=east;{region,amount,year} | 全扫描 | 无 | (east,50,2020),(east,120,2020),(east,200,2021),(east,90,2022)
Q5 region=west∧amount>=200;{region,amount} | V2 | amount>=200 | (west,300)
Q6 空;GROUP BY {};SUM(amount) | V3回滚 | 无 | 单行 SUM=1090（east460+west530+north100）
Q7 amount>=100;GROUP BY {region};SUM | 全扫描（V3拒：amount 非分组列） | 无 | (east,320),(north,100),(west,450)
(甲) [101,+∞)⊆[100,+∞)，被包含，剩余 amount>100；若把 > 误算成 >=（[100,+∞)），north,100,2021 被多留，Q3 变 5 行（多出 (north,100)）。
(乙) V1 物化时丢弃 year，不满足投影列覆盖（投影合法性）；若填 year=0 硬答，按 year 全 0 再按 amount 排序：(east,50,0),(east,120,0),(east,200,0),(east,90,0)，年份与行序全错。
(丙) P 含非分组列 amount 的原子，V3 已丢逐行 amount、无法再按其过滤；若只看 P⊆空谓词就回滚：east 错成 460（正解320），west 错成 530（正解450）。
## 不变量（位置 / 钉住的测试）
I1 与全扫描一致：视图分支与朴素全扫描共用 api.go 的 sel/aggregate/sortRows；selfcheck.go 对内置7查询比对手写期望，TestRewriteMatchesFullScan（region×amount×投影/分组 120 形态）逐行钉住。
I2 包含正确：仅 rewrite.MatchFilter/MatchAgg 中 pred.Contains 通过才选视图，TestViewContainment（含 Q3/Q4/Q5/Q7）钉住。
I3 剩余谓词完备：pred.Residual 逐列取严格更窄原子（VP∧R≡P），TestResidualCompleteness 枚举算子对钉住。
I4 失败不留痕：api.go 的 New/RegisterFilter/RegisterAgg/Query 全部先 validate 再改状态，TestRejectedOpsLeaveState 钉住。
