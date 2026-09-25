# ontology-522 exactly-once 事务输出：推导与不变量

## 八步分步表（新追加条数 / View 变化的 Key / 跳过或拒绝）

1. t1{0a5,1b3,2c7} → 新追加 3；View: a=5 b=3 c=7；无
2. t2{0a9,1d2} → 新追加 2；View: a=9 d=2（a 后写覆盖）
3. t1{3e4} → 新追加 1；View: e=4；同一 txID 追加新 Seq，合法
4. t1{0a5} → 新追加 0；View 不变；幂等跳过
5. t1{1b99} → 新追加 0；View 不变；内容不同仍幂等跳过，b 保持 3
6. t3{0f1,1,"",2} → 新追加 0；View 不变；整批拒绝（空 Key），f 不存在
7. t2{2a11} → 新追加 1；View: a=11；同一 txID 追加新 Seq，合法
8. t2{2a99} → 新追加 0；View 不变；幂等跳过，a 保持 11

- (甲) 第 2 步 a=9。若错把幂等键做成 Key（按 Key 去重）：第 2 步 {0,a,9} 因 key=a 已存在被误跳过、第 7 步 {2,a,11} 同样被误跳过（d=2 等其他记录照常），最终 View()["a"] 错成 5（应为 11）。
- (乙) 第 6 步整批拒绝、f 始终不存在。若非原子逐条落日志：{0,f,1} 先落、随后空 Key 失败不回滚，则 f 错成 1（应不存在）。
- (丙) 幂等键必须细到 (txID,Seq)：第 3 步（t1 的新 Seq3→e）、第 7 步（t2 的新 Seq2→a）都是同 txID 的合法后续批次。粗到 txID 整单判重时：第 3 步被误跳过→e 不存在（应 e=4）；第 7 步被误跳过→a 错成 9（应 11）。

## 四条不变量：保证位置 / 钉住的测试函数

1. 与批量重算一致：olog.Commit 每条追加即时按后写覆盖更新 view，olog.recomputeLocked 从去重日志批量重算供比对 —— TestExactlyOnce（olog_test.go）、TestConcurrentCommit（api_test.go，结束后 View 等于独立批量结果）
2. 精确一次：olog.seen 哈希集合在 append 前判定，命中即不追加 —— TestExactlyOnce（olog_test.go）
3. 幂等：重放命中 seen 即跳过，返回 0 且日志/视图不变 —— TestReplayIdempotent（api_test.go）
4. 失败不留痕：txn.Validate 整批校验先于任何状态修改，通过后才在 olog.Commit 锁内一次性落批 —— TestRejectedBatchLeavesNoTrace（api_test.go，内含三类哨兵可判定且互异的断言）
