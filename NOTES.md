# schema 演进兼容读取：推导与不变量

八步（map 按 a,b,c,d 字典序；被忽略/移除字段不出现）：
1. Write(1,{a:9,b:7}) -> R1
2. Read(R1,3) = {a:9,c:10,d:20}
3. Write(2,{a:5}) -> R2
4. Read(R2,3) = {a:5,c:10,d:20}
5. Write(3,{a:1,c:2,d:3}) -> R3
6. Read(R3,2) = {a:1,b:2,c:2}
7. Read(R2,2) = {a:5,b:2,c:10}
8. Read(R1,1) = {a:9,b:7}

(甲) 第4步 c=10：c 在 R2 缺失，def(c,W=2) 取写入方 v2 默认 10（写时冻结）。错取读取方 R=3 当前默认会得 c=30；错在用新版本默认值篡改旧记录在写入时刻的语义。
(乙) 第6步 b=2：b 在 v3 被移除、R3 无显式值，def(b,W=3) 取移除前最后一次（v2）默认 2。错填零值会得 b=0，丢失写入方历史默认。
(丙) 第2步结果没有 b；若保留读取方已移除字段，b 会以显式值 b:7 错误出现，破坏不变量1（结果只允许含 schema[R] 的字段，须与朴素逐字段解析一致）。

不变量保证位置 / 钉住的测试：
1 朴素参照一致：evol.Engine.Read 只遍历 sch schema[R] 字段，显式值优先否则 sch.Catalog.Def — TestNaiveReference
2 向后兼容无损：Read 只输出共同字段且显式值原样透传（sch.Fields 限定输出域）— TestBackwardCompatible
3 默认值写时冻结：sch.New 预计算 defTab[f][V]，Def 直达、与 R 无关 — TestFrozenDefaults（v1 记录 c 在 R=2/3 恒为 10）
4 失败不留痕：evol.Write 全部校验通过后才加锁建记录，Read 纯函数不改状态 — TestRejectionLeavesNoTrace
复杂度：Def 单次直达，probeCount（sch 非导出字段）记录检查条目数 — TestProbeCountConstant
