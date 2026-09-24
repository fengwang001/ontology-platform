# NOTES

图：A、B 为基视图；C=A+B，D=A*2，E=C+D，F=D-1。值按 A B C D E F 记；“—”=尚无值，“旧”=过期值。

| 步骤 | 脏集合 | A | B | C | D | E | F |
|---|---|---|---|---|---|---|---|
| Set(A,1) | {A,C,D,E,F} | 1 | — | — | — | — | — |
| Set(B,2) | {A,B,C,D,E,F} | 1 | 2 | — | — | — | — |
| Recompute | {} | 1 | 2 | 3 | 2 | 5 | 1 |
| Set(B,20) | {B,C,E} | 1 | 20 | 3旧 | 2 | 5旧 | 1 |
| Recompute | {} | 1 | 20 | 21 | 2 | 23 | 1 |
| Set(A,10) | {A,C,D,E,F} | 10 | 20 | 21旧 | 2旧 | 23旧 | 1旧 |
| Recompute | {} | 10 | 20 | 30 | 20 | 50 | 19 |

（甲）按注册序 A,B,E,F,C,D：脏集 {B,C,E}，E 先于 C 求值，读到旧 C=3 与 D=2，**E 错成 5**（应 23）。

（乙）只标直接下游：Set(A,10) 脏集只剩 {A,C,D}，E、F 不重算，**E 错成 23、F 错成 1**（应 50、19）。

（丙）AddView(M,[N]) 放行（前向引用）；AddView(N,[M]) 必须返回 **ErrCycle**——M 已声明边 M→N，新边 N→M 经前向引用成环，第二次 AddView 整体拒绝、N 不留痕。若环检测忽略前向边，环被放行并潜伏到 Recompute 才爆发或死循环，违反不变量 4。批量参照必须写“按拓扑序”：注册允许前向引用（E、F 早于 C、D），按注册序全量求值时 E 会读到 C/D 的零值或旧值，不满足“按依赖的最终值求值”。

不变量 1（与批量重算一致）：view.go 的 Recompute 先整体校验、再把新值一次性提交；由 TestThreePhases、TestRandomConsistency 钉住。

不变量 2（拓扑序正确）：view.go 在“脏集 ∪ 其依赖”子图上用 dag.go 的 Kahn 拓扑序求值；由 TestThreePhases 钉住。

不变量 3（去重）：view.go 的 dirty 是布尔集合，每脏视图 fn 至多一次，求值数记入非导出字段 evalCount；由 TestDedup 钉住。

不变量 4（失败不留痕）：dag.go 的 Add 先在临时邻接上验环/验重再落边，view.go 所有校验先于任何状态修改；由 TestRejectedOpsNoTrace 钉住。
