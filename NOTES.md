# FKS 两级完美哈希 — 推导与不变量

键集 {5,11,13,17,19,24}，m=6，p=29。
1. 5：h1=5 mod6=5 -> 桶5
2. 11：h1=11 mod6=5 -> 桶5
3. 13：h1=13 mod6=1 -> 桶1
4. 17：h1=17 mod6=5 -> 桶5
5. 19：h1=19 mod6=1 -> 桶1
6. 24：h1=24 mod6=0 -> 桶0（直接槽）
7. 桶1{13,19} n=2,mj=4：a=1,b=0 即无碰撞；槽 13->1，19->3
8. 桶5{5,11,17} n=3,mj=9：a=1,b=0 即无碰撞；槽 5->5，11->2，17->8

(甲) mj 错用 3：5、11、17 mod3 全为 2，三个键同撞槽 2。
(乙) Lookup(7)：7 mod6=1、7 mod4=3，槽3存 19；漏比对会误报找到 19；正确结果=(false, ErrNotFound)。
(丙) 桶1 漏掉最后的 mod4：13->槽13、19->槽19；表只有槽 0..3，两个槽位均越界。

## 不变量（代码保证位置 / 钉住的测试函数）

- I1 无碰撞：second/second.go 的 Build 仅在桶内两两异槽时固定 (a,b)，Lookup 必须比对槽内键；TestSixKeyBuckets、TestBuildLookup。
- I2 同朴素参照：api/api.go 的 Lookup 命中比键，空槽/异键一律 ErrNotFound；TestReferenceConsistency、TestConcurrentLookup。
- I3 空间 Σnj²：second/second.go 建表长度严格 n_j²、直接槽不建表（计0）；TestSpaceSum、TestSelfCheck。
- I4 失败不留痕：api/api.go 的 Build 先全量校验、在本地构建成功后才整体替换状态；TestSentinelErrors、TestRejectedState、TestSelfCheck。
