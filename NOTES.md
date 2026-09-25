# NOTES — exactly-once 事务输出

## 八步推导（+条数 / View 变化 / 判定）
1. +3；a=5 b=3 c=7；全新
2. +2；a=9 d=2；t2 是新事务，(t2,0)≠(t1,0)，a 后写覆盖
3. +1；e=4；(t1,3) 是新 Seq
4. +0；无变化；(t1,0) 重放，幂等跳过
5. +0；无变化（b 仍=3）；(t1,1) 已提交，内容不同也跳过
6. +0；整批拒绝 ErrEmptyKey；f 不存在，状态不变
7. +1；a=11；(t2,2) 是新 Seq
8. +0；无变化（a 仍=11）；(t2,2) 重放跳过

终态：a=11 b=3 c=7 d=2 e=4；f 不存在。

(甲) 第2步 a=9。若错按 Key 去重：第2步 {0,a,9} 因 a 已在而被跳过、第7步 {2,a,11} 也被跳过，View["a"] 错成 5（正解 11）。
(乙) 第6步整批拒绝、f 不存在。若非原子逐条落盘：{0,f,1} 先落，f 错成 1（正解：不存在）。
(丙) 若粗到按 txID 判重：第3步 t1 已见 → e 被误跳过，e 错成不存在（正解 e=4）；第7步 t2 已见 → {2,a,11} 误跳过，a 错成 9（正解 11）。

## 不变量 → 代码保证位置 / 钉住测试
I1 与批量重算一致：olog.Commit 在同一把锁内按追加序执行 view[k]=v；api 层 TestViewBatchRecompute 逐操作对独立重算结果比对。
I2 精确一次：olog 的 done map[idKey] 判定，仅未命中的记录才 append；log_test.go TestLogExactlyOnce 数 entries 长度。
I3 幂等无操作：txn.Plan 用谓词把已提交键过滤出 fresh，跳过分支零写入；api 层 TestRandomReplay 随机序重放恒 added=0、视图不变。
I4 失败不留痕：olog.Commit 先跑纯函数 txn.Validate（锁外），通过后才在锁内改状态；olog.TestRejectAtomic 钉住三类拒绝且日志/视图/已提交集合长度不变（含哨兵两两互异断言），api.TestViewBatchRecompute 用随机非法批复核。
