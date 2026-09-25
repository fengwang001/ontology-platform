# Rabin-Karp 推导

设滚动哈希为 `h = (h*base + c) % mod`，长度为 `m` 的窗口可滚动更新。
模运算是多对一映射，所以两个不同子串可能得到同一个哈希值。
因此 `hash(text窗口) == hash(pattern)` 只是匹配的必要条件，不是充分条件。
命中哈希后必须从窗口起点逐字节比较；全部相等才可记录位置。

窗口滚出最高位字符 `c` 时，它的权值是 `base^(m-1)`，所以先计算
`next = h - c*base^(m-1) + newByte`。整数取模在减法后可能为负。
负余数若直接更新，会让后续哈希符号和范围错乱。
正确做法是先加一个足够大的模倍数再取模，或用等价归一化：
`next = (next%mod + mod) % mod`，保证结果始终落在 `[0,mod)`。

碰撞测试固定使用 `base=2, mod=251`：
`" \""` 与 `"! "` 的哈希均为 98，但逐字符比较会排除假命中。

实现索引：
- `hash/hash.go:37`、`hash/hash.go:41`、`hash/hash.go:47` 提供 Append/Remove/Value。
- `match/match.go:10` 定义 `ErrEmptyPattern`；`match/match.go:19` 实现 FindAll。
- `match/match.go:41` 先比哈希再调用逐字符验证；`match/match.go:49` 统计验证次数。
- `check/check.go:3` 是朴素参照。
- `TestFindAll` 覆盖全部位置、重叠、相同、无出现、pattern 更长、空 pattern 和朴素一致性。
- `TestCollision` 同时钉住正确实现与「命中即成功」错误实现。
- `TestVerificationBound`、`TestConcurrent` 覆盖复杂度上界与并发。
