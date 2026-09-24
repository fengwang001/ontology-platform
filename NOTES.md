# Changelog compaction：八步推导与不变量

## 八行分步表（逐条列日志；K1..K4）
1. `1:K1=10`
2. `1:K1=10 2:K2=20`
3. `1:K1=10 2:K2=20 3:K1=30`
4. `1:K1=10 2:K2=20 3:K1=30 4:K3=40`
5. `1:K1=10 2:K2=20 3:K1=30 4:K3=40 5:K1=50`
6. `…5:K1=50 6:K2=60`
7. `…6:K2=60 7:K4=70`
8. 原条目 `1:K1=10 2:K2=20 7:K4=70`；压缩记录 C[3,7)（自 lo=3 起可见，seq<3 不可见）：K1=50、K2=60、K3=40

- (甲) 第8步后 Read(5,K1)=**50**，Read(6,K2)=**60**。错守「区间内每 Key 留第一条」：K1 错成 30（seq3）、K2 错成 20（seq2）。
- (乙) 再答一次仍为 **50、60**（记录自 lo=3 可见，5、6 均 ≥3）。错把可见性写成 seq≥hi(=7)：seq5/6 记录不可见，区间原条目已删，只剩区间外 → K1 错成 10（seq1）、K2 错成 20（seq2）。
- (丙) compact 前 Read(3,K1)=30、Read(5,K1)=50；compact 后两者都=50。丢掉的是 Seq=3 的 K1=30（被位点5的 K1=50 覆盖，区间内坍成一个值）。K4=70 不受波及：区间 [3,7) 左闭右开，hi=7 排他，Seq=7 ∉ 区间。

## 四条不变量（代码位置 / 钉住的测试）
1. 与批量参照一致：`cpt.go` 折叠取每 Key 区间内最后写入 + `clog.go` 记录自 lo 可见，`api.go` Read 在可见条目中取 Seq 最大 → `TestBatchReference`
2. 区间外不动：`cpt.go` 仅替换 seq∈[lo,hi) 的原条目，区间前原条目保留且压缩记录对 seq<lo 不可见（`clog.go`）→ `TestOutsideUnchanged`
3. 位点保持可寻址：`cpt.go` 的 siteLo 位点映射覆盖每个 seq，`api.go` Read 经映射定位后每 seq∈[1,nextSeq) 均合法 → `TestSitesAddressable`
4. 失败不留痕：`api.go` Append/Read/Compact 全部先校验（ErrEmptyKey/ErrSeqOutOfRange/ErrBadRange 三个互异哨兵）后改状态 → `TestRejectedOpsNoTrace`

另：区间外访问计数（`cpt.go` 非导出 skippedOutside）→ `TestOutsideVisitCounter`；并发读对照串行 → `TestConcurrentReads`。
