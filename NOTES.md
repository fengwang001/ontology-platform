# ontology-271 NOTES（CDC 前像校验与冲突分类）

记号：`1{ann,4}`=PK1{name:ann,qty:4}，`3{cid}`=PK3{name:cid}（无 qty 列）。初始 1{ann,3};2{bob,5};3{cid}，maxRows=4，C=冲突日志条数。

| 步 | 判定 | 本步之后副本全部行 | C |
|---|---|---|---|
| 1 Seq1 Update1 前像{ann,3}→{ann,4} | 已应用 | 1{ann,4};2{bob,5};3{cid} | 0 |
| 2 Seq2 Update2 前像qty:6 | 前像不符 | 1{ann,4};2{bob,5};3{cid} | 1 |
| 3 Seq3 Delete3 前像{cid,qty:""} | 前像不符（缺列≠空串） | 1{ann,4};2{bob,5};3{cid} | 2 |
| 4 Seq4 Insert2{bea,1} | 行已存在 | 1{ann,4};2{bob,5};3{cid} | 3 |
| 5 Seq5 Delete1 前像{ann,3} | 前像不符 | 1{ann,4};2{bob,5};3{cid} | 4 |
| 6 Seq6 Update4(行不存在) | 行不存在 | 1{ann,4};2{bob,5};3{cid} | 5 |
| 7 Seq7 Insert4{dan,2} | 已应用（4 行≤4） | 1{ann,4};2{bob,5};3{cid};4{dan,2} | 5 |
| 8 Seq8 Update4 前像{dan,2}→{dan,3} | 已应用 | 1{ann,4};2{bob,5};3{cid};4{dan,3} | 5 |

(甲) Update/Delete 只看主键：步2/3/5 被盲改盲删。终态：2{bob,7};4{dan,3}。冲突仅 Seq4 行已存在、Seq6 行不存在，共 2 条。
(乙) 仅 Delete 不校验前像：步3、步5 盲删；步2 仍冲突。终态：2{bob,5};4{dan,3}。冲突：Seq2 前像不符、Seq4 行已存在、Seq6 行不存在，共 3 条。
(丙) 记冲突后仍应用：步6 把 PK4 盲插成功，致**步7 Insert PK4 错判成「行已存在」**（正确应为已应用）。终态：2{bea,1};4{dan,3}。冲突 Seq2–Seq7 共 6 条。

不变量 → 代码保证位置 / 钉住测试：
1. 与盲应用一致：rapply.go 的 `Apply` 只在 applied 分支写工作副本（Insert/Update 写 After、Delete 删除），提交态即「只取已应用事件盲写」→ TestBlindApplyRandom、SelfCheck。
2. 判定可复算：rapply.go 的 `judge` 严格按「主键存在性 → `rimg.RowEqual` 整行前像」顺序，对提交前工作副本判定 → TestEightEvents。
3. 冲突零副作用且结果完备：`Apply` 冲突分支只追加 staged 日志、不动工作副本；每事件恰生成一个 Result → TestConflictZeroSideEffect、TestEightEvents。
4. 失败不留痕：`Apply` 先校验形状与序号，再在 clone 出的工作副本上处理，超限即丢弃副本，只有全程成功才提交 rows/lastSeq/conflicts → TestAtomicReject、TestSentinelErrors。
读取计数 `reads` 为 rapply 非导出字段（每次判定恒为按 PK 读取 1 行），仅同包代码（TestReadCountBounded、ReadBoundOK）直接读取；SelfCheck 经 ReadBoundOK 对 m=100..10000 各档断言有界，数值不经任何导出接口外泄。
