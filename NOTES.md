# NOTES

## 推导：碰撞回退与取模

滚动哈希把窗口的 m 个字符压成一个整数：`h = (h*base + c) % mod`。
哈希相等只是匹配的**必要条件而非充分条件**：mod 有限，不同子串
可能同余（鸽巢原理，碰撞必然存在）。因此哈希命中后必须回退逐字符
验证，验证通过才算匹配，否则碰撞会被误判为匹配（假阳性）。

滚动移出最高位字符 c（窗口长 m，c 的权重为 base^(m-1)）：
`h' = h - c*base^(m-1) (mod mod)`。当 `c*base^(m-1) % mod > h` 时
直接相减得到负数（或有符号溢出），哈希值就此错乱。正确做法是先加
一个 mod 再取模：`h' = (h + mod - (c*pow) % mod) % mod`，结果恒
为非负且同余，与逐字符重算完全一致。

## 语义 → 代码与测试位置

1. 所有出现（含重叠、升序）：`match.Find`（match/match.go:37）→ TestFindAll
2. 无假阳性：命中后 `verify` 逐字符验证（match/match.go:66）→ TestCollision
3. 空 pattern 报 `ErrEmptyPattern`（:39）；长于 text 返回空（:42）→ TestFindAll
4. 确定性：纯函数 → TestFindAll（重复调用）、TestConcurrent（-race）
5. 边界：相同串 [0]、无出现空 → TestFindAll

## 其他钉住点

- 取模不出负数：`Remove` 先加 mod 再减（hash/hash.go:47）→ TestRollingHash
- 碰撞用例：4 个零字节与 mod 的 256 进制数字串哈希相等；内联错误
  实现 `check.hashHits`（check/check.go:22）命中即成功 → TestCollision
- 复杂度：`match.comparisons`（match/match.go:22）→ TestComparisonBound
- 哨兵错误 ×3 均可 errors.Is 区分 → TestFindAll、TestRollingHash
