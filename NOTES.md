# 变更审计日志 NOTES

## 七步推导（创世条目 Seq=0, TS=0 之后）

| 步 | 请求 (TS,Who,Op) | 结果 | Seq | 条目总数(含创世) |
|---|---|---|---|---|
| 1 | (10,"A","put") | 接受 | 1 | 2 |
| 2 | (12,"A","get") | 接受 | 2 | 3 |
| 3 | (12,"B","del") | 接受（TS 相等允许） | 3 | 4 |
| 4 | (9,"A","put") | 拒绝：TS=9 < 前一条 TS=12（乱序） | 无 | 4 |
| 5 | (15,"B","put") | 接受 | 4 | 5 |
| 6 | (15,"","get") | 拒绝：Who 为空串 | 无 | 5 |
| 7 | (20,"A","del") | 接受 | 5 | 6 |

- (甲) 非递减（`TS >= 前一条`）下第 3 步被接受，相等允许。若误写成严格递增（`TS > 前一条`），第 3 步会被拒绝，于是第 5 步错拿 Seq=3、第 7 步错拿 Seq=4（正确值分别为 4、5）。
- (乙) 第 4 步被拒后正确实现下第 5 步拿 Seq=4。若被拒的 Append 也消耗一个 Seq 号，第 4 步会空耗 Seq=4，第 5 步错拿 Seq=5，日志在 Seq=4 处留下空洞（Seq 不再连续）。
- (丙) 把 Seq=3 的 Op 由 "del" 改成 "put"、不动任何存储 Hash：Verify 从创世连续重算，在 Seq=3 处重算值≠存储值，返回 3；Affected(3)=[3,4,5]。若用成对校验（每条只比对前一条的存储 Hash），Seq=4、5 各自成对检查都通过，Affected 只剩 [3]，漏掉 [4,5]。

## 四条不变量：保证位置与钉住测试

1. 顺序正确：`log.Log.Append` 先校验再取 `prev.Seq+1` 分配 Seq，并判定 `TS >= prev.TS`；测试 `TestAppendSequence`、`TestRejectNoTrace`。
2. 与朴素重算一致：`log.Log.Verify` 从创世条目逐条重算哈希，返回首个失配 Seq，全对返回 -1；测试 `TestVerifyDetectsTamper`（含多点篡改取最小者）。
3. 哈希链自洽：`ent.ComputeHash` 定义 `sha256(prev.Hash‖Seq‖TS‖Who‖Op)`，Append 用缓存链头接续；测试 `TestHashChainConsistent`（逐前缀核验）。
4. 失败不留痕：`log.Log.Append` 全部校验通过前不写任何状态，四类拒绝各返回不同哨兵错误；测试 `TestRejectNoTrace`、`TestErrorsDistinct`。
