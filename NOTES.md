# ontology-440 NOTES: sticky-session monotonic reads

建立: A@1,B@2; Offline R2; C@3; Offline R1; D@4,E@5; Online R1,R2 → R0/R1/R2 applied=5/3/2, k=E/C/B。S 粘 R0, seen=0。
五行（seen 前→后；补的 lsn；目标副本 applied 前→后；Read 返回）:
1. Read(S,k)        seen 0→5 | 补:无        | R0 5→5 | 返回 (E,5)
2. Switch S→R1      seen 5→5 | 补 lsn 4,5  | R1 3→5 | 无返回
3. Read(S,k)        seen 5→5 | 补:无        | R1 5→5 | 返回 (E,5)
4. Switch S→R2      seen 5→5 | 补 lsn 3,4,5| R2 2→5 | 无返回
5. Read(S,k)        seen 5→5 | 补:无        | R2 5→5 | 返回 (E,5)
(甲) Switch 不补全: 切完瞬间 R2 停在 (B,2)，违反不变量3；但题给 Read 规则自带 catch-up，故第5行受守卫的 Read 仍补 (2,5] 返回 (E,5)，该次不回退；绕过会话守卫的直读（如 View 副本）会拿到错值 (B,2)，版本 5→2 回退，违反单调读。
(乙) 无粘滞轮询: 读 R0 得 (E,5) 后第二次轮到 R1（applied=3,k=C）读到错值 (C,3)；单调读正确值为 (E,5)；5→3 回退，违反单调读。
(丙) catch-up 用开区间 (applied,seen): 第2行切 R1 只补 lsn4(D)，applied=4；第3行 Read 再求 (4,5)=∅，返回错值 (D,4)；正确为 (E,5)；5→4 回退，违反单调读。

不变量 → 代码保证位置 → 钉住的测试函数:
1 单调读: api.Read 调 CatchUp 到 seen，log.Range 在 applied>=seen 时返回空（即"落后才补"），读后 seen=max(seen,applied)（api.go Read + log.go Range）→ TestMonotonicRead
2 与朴素参照一致: Write 同步更新朴素 ref，View 返回 ref 拷贝，catch-up 按 lsn 重放与全局日志等价（api.go 的 Write/View/Read）→ TestReferenceEquivalence
3 补全到位: SwitchReplica 校验通过后先 CatchUp 到 seen、最后才改 sess.replica（api.go 的 SwitchReplica）→ TestSwitchCaughtUp
4 失败不留痕: 所有哨兵校验先于任何状态赋值，idx/online 检查在 CatchUp 之前（api.go 的 Write/Read/SwitchReplica/setStatus）→ TestRejectedOpsLeaveNoTrace
复杂度: rep.Replica.lastScan（非导出）记录上次 catch-up 经 log.Range 按切片下标直接定位所检查的条目数 → rep_test.go 的 TestCatchUpScanBound
