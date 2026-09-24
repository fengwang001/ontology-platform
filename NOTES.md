# INTERSECT ALL 增量视图推导与不变量

值 "a" 八步表（本步无变更记「无」）：

| 步 | 操作 | l | r | m | 本步输出 |
|---|---|---|---|---|---|
| 1 | Apply(L,"a",+1) | 1 | 0 | 0 | 无 |
| 2 | Apply(R,"a",+1) | 1 | 1 | 1 | +(a) |
| 3 | Apply(R,"a",+1) | 1 | 2 | 1 | 无 |
| 4 | Apply(L,"a",+1) | 2 | 2 | 2 | +(a) |
| 5 | Apply(R,"a",+1) | 2 | 3 | 2 | 无 |
| 6 | Apply(R,"a",−1) | 2 | 2 | 2 | 无 |
| 7 | Apply(R,"a",−1) | 2 | 1 | 1 | −(a) |
| 8 | Apply(L,"a",−1) | 1 | 1 | 1 | 无 |

(甲) 第 3 步正确 m=1。若每个 +1 都无条件发 +(a)，第 3 步后 m 被错推为 3；多输出在第 1、3、5 步（都是盈余侧 +1，正确输出只在第 2、4 步穿越时发生）。
(乙) 第 6 步正确 m=2（r:3→2 仍 r≥l，未穿越）。无条件发 −(a) 会把 m 错降为 1；对应「穿越才变」：从较大一侧删除、删后仍不小于另一侧时 m 不变、不输出。
(丙) NULL：两侧各 +1 后 m 恒 0、两步皆「无」；把 NULL 当相等会在 R+1 时错输出 +(<null>)。第 5 步正确输出「无」(0 条)；把盈余 r−l=1 当交集会错输出 1 条 +(a)（m 虚增为 3）。

不变量 → 代码保证位置 / 钉住它的测试函数：

I1 批量重算一致：mset.go `Cell.Apply` 取 oldM/newM=min(l,r) 之差；inter.go `View` 逐值取 M 且仅留 >0（NULL 恒 0 自然排除）→ TestViewMatchesBatchReplay。
I2 变更日志自洽：inter.go `Apply` 返回带符号 delta（被拒则返回 0 且不改状态），api.go `Apply` 按 |delta| 展开为同号 Change、整批返回；任一前缀下游计数非负、− 撤回的恰为现存副本 → TestChangeLogPrefixes。
I3 穿越才变：mset.go `Cell.Apply` 仅返回 newM−oldM，较大侧增减时差为 0 → TestCrossingEightSteps。
I4 失败不留痕：mset.go `Cell.Apply` 先判 d==0 与将变负、后改计数；inter.go `Apply` 先判空串再查图，三个哨兵错误互异 → TestErrorsDistinctAndAtomic。
