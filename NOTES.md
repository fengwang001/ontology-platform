# NOTES：多源并集增量去重（nPart=2，元素 a/b）

|步|操作|cnt[a]|cnt[b]|changelog|View|
|---|---|---|---|---|---|
|1|Add(0,a)|1|0|+a|{a}|
|2|Add(1,a)|2|0|无|{a}|
|3|Add(0,b)|2|1|+b|{a,b}|
|4|Remove(0,a)|1|1|无|{a,b}|
|5|Remove(1,a)|0|1|-a|{b}|
|6|Remove(0,b)|0|0|-b|{}|
|7|Add(1,b)|0|1|+b|{b}|
|8|Remove(0,b)|0|1|无|{b}|

(甲) 步4 cnt[a]=2→1，无输出；错版「任一 Remove 立即撤回」会输出 -a、View 错成 {b}（分区1仍持有 a），步5还会再错输出一条 -a（撤回已撤回元素）。
(乙) 步8 b 不在分区0（幂等 no-op）；错版无条件转发会输出 -b、View 错成 {}，实际 b 仍被分区1持有，应为 {b}。
(丙) 步2 a 已在视图；错版「每次 Add 都 +」会再输出一条 +a，下游把 a 数成 2 份（无 - 间隔的重复 +）。

不变量 → 代码保证位置 / 钉住测试：
1 与批量重算一致：uni.Add/Remove 只随成员变更改 cnt，uni.View 仅取 cnt>=1；TestViewMatchesBatchUnion
2 changelog 自洽：仅 0→1 追加 +、1→0 追加 -（uni.go Add/Remove）；TestChangelogAlternation
3 引用计数守恒：集合增删与 cnt++/-- 成对出现在同一函数（uni.go）；TestRefCountConservation
4 失败不留痕：api.New/Add/Remove 先校验后变更 + 哨兵错误；TestRejectedOpsNoTrace
