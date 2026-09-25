# chunked 增量快照导出：推导与不变量

状态 a=1 b=2 c=3 d=4 e=5 f=6，chunkSize=2；Put(d,99) 发生在第 2、3 步之间，只改实时态。

| 步 | cursor 入参 | 返回块 | newCursor | done |
|---|---|---|---|---|
| 1 Snapshot() | — | 固化 a..f（d=4） | — | — |
| 2 Next("") | "" | a=1,b=2 | b | false |
| 3 Next("b") | b | c=3,d=4（快照值，非 99） | d | false |
| 4 Next("d") | d | e=5,f=6 | f | true |
| 5 Next("f") | f | 空块，err=ErrFinished | —（未前进） | — |

(甲) 若写成 >=：Next("b") 多导出 b（块变成 b=2,c=3）；断点续传把 newCursor 传回后，b 被导出两次。
(乙) 若是活视图：Next("b") 会读到 d=99；正确值为快照时刻的 d=4。
(丙) 用「本块条数 < chunkSize」判 done：第 4 步是满块，done 错成 false，必须第四次 Next 拉空块才 done；n 为 chunkSize 整数倍时根本不存在短块，正确判据是 newCursor 之后无严格更大键，末块满也 done=true。

不变量（代码保证位置 / 钉住测试）：
I1 拼接==快照全量：export/export.go 的 Next 在有序切片上顺序取 [lo:end)；TestInvariantNaiveRef。
I2 无重复无遗漏不倒退：snap/snap.go 的 After 严格 > 二分定位、newCursor 即本块末键；TestInvariantNoDupNoGap。
I3 点时刻一致：snap/snap.go 的 Capture 深拷贝、api/api.go 的 Snapshot 持锁拷贝；TestInvariantPointInTime（并发版 TestConcurrentPointInTime）。
I4 失败不留痕：api/api.go 所有校验（块大小/空键/未快照/已完成）都先于任何状态变更；TestRejectedOpsLeaveNoTrace。
四类哨兵互不相同：TestSentinelErrorsDistinct；复杂度白盒核验：export/export_test.go TestProbeCountSublinear（probeCount 无任何公开读取口，只暴露布尔结论）。
