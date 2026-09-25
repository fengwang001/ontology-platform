# 物化视图重写器推导（行序 region→year→amount；投影列序 region,amount,year）

| 查询 | 命中 | 剩余谓词 | 结果行 |
|---|---|---|---|
| Q1 region=east, proj{r,a} | V1 | 无 | (east,50),(east,90),(east,120),(east,200) |
| Q2 region=east AND a>=100, proj{r,a} | V1 | amount>=100 | (east,120),(east,200) |
| Q3 amount>100, proj{r,a} | V2 | amount>100 | (east,120),(east,200),(west,150),(west,300) |
| Q4 region=east, proj{r,a,y} | 全扫描 | — | (east,50,2020),(east,120,2020),(east,200,2021),(east,90,2022) |
| Q5 region=west AND a>=200, proj{r,a} | V2 | region=west,amount>=200 | (west,300) |
| Q6 空谓词 GROUP BY{} SUM | V3 回滚 | 无 | SUM=1090 |
| Q7 a>=100 GROUP BY{region} SUM | 全扫描 | — | east=320,north=100,west=450 |

(甲) `>100`=[101,+∞) ⊆ `>=100`=[100,+∞)，被包含，剩余 amount>100；若误算成 [100,+∞)，则多出 north 那行 (north,100)，Q3 由 4 行变 5 行。
(乙) V1 物化时丢弃了 year，投影 {region,amount,year} 不被列集合 {region,amount} 覆盖；若填零硬答，四行错成 (east,50,0),(east,90,0),(east,120,0),(east,200,0)。
(丙) P 含非分组列 amount 的原子，聚合视图已无逐行 amount，禁止回滚；若只看 P⊆空谓词就回滚，east=460（应 320，混入 50+90）、west=530（应 450，混入 80）。

不变量保证位置 / 钉住测试：
1. 与全扫描一致：rewrite.Query/queryAgg 的视图路径与全扫描路径产出后共用同一 `sortRows`，api 输出再按 keep 裁剪；由白盒 TestEquivRewrite（500 组随机谓词×投影/分组，对照空 catalog 全扫描）、api `SelfCheck`、TestSevenQueries 钉住。
2. 包含正确性：唯一闸口 rewrite `pick` 仅在 `pred.Contains(p,v.vp)` 后选视图 —— TestContainment、TestSevenQueries。
3. 剩余谓词完备：`pred.Residual` 逐列保留严格更窄原子；普通查询在 Query 对视图行应用 res，聚合回滚在 queryAgg 用 `res.Match(groupKey)` 先过滤分组（amount 剩余在 pick 即拒）—— TestResidualEquivalence 逐行核验 `Vp∧剩余 ≡ P`。
4. 失败不留痕：api `prep` 在加锁/`Catalog.Add` 之前完成全部校验，`New` 遇错直接返回不建引擎 —— TestRejectedLeavesState、TestSentinelErrors。
复杂度：rewrite 非导出字段 `compares atomic.Int64`，region 等值哈希桶 `bucket` 定位候选；白盒 TestComparesBounded 断言 m=100/1000/10000 时比较数恒 ≤2、不随 m 增长（无 region 原子时确实比较全部 m，证明计数器真实）。
