# NOTES — ontology-353 分层布隆去重（m=8, cap=2, k=2）

位位置 h1=x mod 8, h2=(5x+3) mod 8：a{0,1} b{2,5} c{2,3} d{4,7} i{0,1} f{1,6}

| 步 | 键 | 切换 | 当前层位集(升序) | 历史层位集(升序) | 判定 |
|---|---|---|---|---|---|
| 1 | a | 否 | {0,1} | {} | 新 |
| 2 | b | 否 | {0,1,2,5} | {} | 新 |
| 3 | c | 是 | {2,3} | {0,1,2,5} | 新 |
| 4 | d | 否 | {2,3,4,7} | {0,1,2,5} | 新 |
| 5 | i | 是 | {} | {0,1,2,3,4,5,7} | 重复(假阳性,不写入) |
| 6 | f | 否 | {1,6} | {0,1,2,3,4,5,7} | 新 |
| 7 | a | 否 | {1,6} | {0,1,2,3,4,5,7} | 重复(真) |
| 8 | b | 否 | {1,6} | {0,1,2,3,4,5,7} | 重复(真) |

(甲) 假阳性：i 的两个位置 {0,1} 与历史层中 a 封存的位完全重合（第5步切换已把 a,b,c,d 的位 OR 进 history）。若切换只清当前层不并入历史（history 恒空）：第5步 i 判「新」（本该假阳性为「重复」），第7步 a 判「新」——a 被第二次输出「新」即假阴性，违反不变量1。

(乙) 覆盖而非 OR：第5步切换后 history 错成 {2,3,4,7}（a,b 的位 {0,1,5} 被清除）；其后第8步 b 需要 {2,5}，位 5 已丢，判「新」=假阴性。清除已置位违反不变量2（历史=各封存层并集）与不变量3（历史位单调不减）。

(丙) 历史层累计 4 个键（a,b,c,d；i 是假阳性不写入，f 仍在当前层）。正确 (1−(7/8)^(2·4))²=(1−(7/8)^8)²≈43.1%；错按单层 cap=2：(1−(7/8)^4)²≈17.1%——从约 43.1% 低估为约 17.1%。

不变量1（无假阴性）：`dedup.(*Deduper).step` 仅在 current、history 两层 Contains 皆否时判新——钉于 TestNoFalseNegatives。
不变量2（切换正确）：`switchIfFull` 先 `history.Merge(current)` 再 `current.Reset()`，histN 同步累加——钉于 TestSaturateSwitch。
不变量3（历史只增/FP 单调）：history 只经 Merge 做 OR 累积、从不 Reset；EstimateFP 以 cur.Count()/histN 精确代公式——钉于 TestHistoryMonotoneAndFP。
不变量4（失败不留痕）：bf.New/dedup.New 返回哨兵错误；`feed` 先整批校验空键、通过后才落位，任一空键整批不动——钉于 TestRejectedOpNoTrace。
复杂度：非导出 lastProbeCount 在每次层 Contains 查 k=2 位后 +2，单层查两位不短路，恒为 4——钉于 TestProbeCountConstant；并发只读一致——钉于 TestConcurrentReaders。
