# NOTES — SEMI JOIN 存在性增量维护

## 八步推导表（maxLeft=8，L1..L4）

| 步 | 操作 | 各键 ref | 保留集（升序） |
|---|---|---|---|
| 1 | AddLeft(L1,"a") | （无） | {} |
| 2 | AddLeft(L2,"a") | （无） | {} |
| 3 | AddRight("a") | a=1 | {L1,L2} |
| 4 | AddLeft(L3,"b") | a=1 | {L1,L2} |
| 5 | AddRight("b") | a=1,b=1 | {L1,L2,L3} |
| 6 | AddLeft(L4,nil) | a=1,b=1 | {L1,L2,L3} |
| 7 | DelRight("a") | a=0,b=1 | {L3} |
| 8 | AddRight(nil) | a=0,b=1,NULL=1 | {L3} |

(甲) 再 AddRight("a")（ref 1→2）后半连接视图仍 {L1,L2}，总出现 2 次；内连接按匹配行数展开为 L1×2+L2×2=4 次，出错的 2 次是 L1、L2 各被重复输出一次。
(乙) L4 不进入视图，第 8 步视图为 {L3}；若把 NULL 当可匹配（NULL==NULL，或 NULL 等同空串），视图错成 {L3,L4}，多出 L4。
(丙) 把 AddRight("a") 提到最前，最终视图相同 {L3}；差异在点亮日志：新顺序下 L1、L2 在各自 AddLeft 时即点亮（到达时 ref["a"] 已 ≥1），原顺序中二者在第 3 步 ref 0→1 边沿同时点亮、第 7 步 1→0 同时熄灭——差异来自「AddLeft 即判存在性」与「ref 0↔1 边沿批量切换」两条规则。故不变量 1 的参照只能按 ref≥1 的存在性、每行至多一次：半连接真值是左行集合而非左右行笛卡尔积，右表重复行不得改变它。

## 四条不变量：代码保证位置 + 钉住的测试函数

1. 与批量重算一致：semi/semi.go 中 `lit` 只在 AddLeft/AddRight/DelRight 内按「键非 nil 且 ref≥1」维护，`View` 返回 `lit` 的升序副本；由 TestViewMatchesBatch 钉住。
2. 半连接不重复：`byKey` 键索引 + `lit` 以 id 为键，仅在 ref 的 0↔1 边沿切换，ref>1 不重复加入；由 TestNoDuplicationWhenRefMultiplied 钉住。
3. 撤回不越界 / NULL 不匹配：DelRight 先校验后改值（NULL 走独立 nullRef，同样禁止变负），nil 键永不进入 byKey/ref 点亮路径；由 TestRefBoundsAndNull 钉住。
4. 失败不留痕：所有写操作持写锁后先完成全部校验、再修改任何 map（重复 id / 超限 / 不存在 / 越界）；由 TestRejectedOperationsAtomic 钉住。

复杂度：非导出字段 lastScan 记录最近一次右表操作在受影响键下检查的左行数，同包测试 TestRightLookupCounterIndexed 直接读取；并发由 sync.RWMutex 保证，TestConcurrentReadersAgree 钉住。
