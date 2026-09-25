# NOTES

schema：v1 `a=1,b=2`；v2 `a=1,b=2,c=10`；v3 `a=1,c=30,d=20`。缺失默认 `def(f,W)` 写时冻结。

八步表（字段按字典序；写入步无 Read map）：

1. `Write(1,{a:9,b:7})` → R1
2. `Read(R1,3)` = `{a:9,c:10,d:20}`
3. `Write(2,{a:5})` → R2
4. `Read(R2,3)` = `{a:5,c:10,d:20}`
5. `Write(3,{a:1,c:2,d:3})` → R3
6. `Read(R3,2)` = `{a:1,b:2,c:2}`
7. `Read(R2,2)` = `{a:5,b:2,c:10}`
8. `Read(R1,1)` = `{a:9,b:7}`

(甲) 步4 `c=10`：缺失取 `def(c,W=2)=10`。错取读取方当前默认则 `c=30`——v3 的重定义被错误泄漏进 v2 写的旧记录，违反写时冻结。
(乙) 步6 `b=2`：b 在 W=3 已移除，取移除前最后一次默认（v2 的 2）。错填零值则 `b=0`。
(丙) 步2 结果中**没有 b**：schema[R=3] 不含 b，写入方多余字段一律忽略。错保留则 `b:7`（R1 的显式值）混入结果，破坏第二节**不变量 1**（与朴素参照一致：朴素解析只遍历 schema[R] 字段）。

不变量的保证位置与钉住测试：

1. 与朴素参照一致：`api/api.go` 的 `naiveRead`（用 `sch.Exact` 独立逐版本扫描）+ `SelfCheck` 双路径逐字段比对；`TestSelfCheck` 钉住。
2. 向后兼容无损：`evol/evol.go` 的 `Read` 只遍历 schema[R] 字段、共同字段直取显式值；`TestBackwardCompatible` 钉住。
3. 默认值写时冻结：`sch/sch.go` 的 `freeze` 表只按写入版本 W 索引、与 R 无关；`TestFrozenDefaults` 钉住。
4. 失败不留痕：`evol/evol.go` 的 `Write` 先全量校验、通过后才复制 map 并 append；`TestRejectionLeavesNoTrace` 钉住。

复杂度计数器 `probe` 为 `sch` 内非导出字段（`atomic.Int64`），数值不经任何导出接口暴露；`sch/sch_test.go` 的 `TestProbeConstantBound` 钉住 m=100/1000/10000 下检查数恒为常数。
