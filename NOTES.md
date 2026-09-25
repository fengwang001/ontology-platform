# Rabin-Karp 推导与索引

## 第三节推导：碰撞回退与取模
滚动哈希 `h = (h*base + c) % mod` 把窗口内容压缩成一个整数，
哈希相等只是匹配的**必要条件而非充分条件**：mod 有限，
由鸽巢原理必然存在不同子串同余（碰撞），
故哈希命中后必须回退逐字符验证，否则碰撞即假阳性；
验证保证返回的位置都是真实出现，且验证只发生在哈希命中时。
滚动删除最高位时 `h = h - c*base^(m-1) (mod mod)`，
当 `h < c*pow` 直接相减会得到负数（无符号下回绕错乱），
正确做法：`h = (h - c*pow%mod + mod) % mod`，先加 mod 再取模，
结果恒落在 `[0, mod)`，与从头逐字符计算完全一致。
溢出：取 `mod=1e9+7`、`base=91138233`，`h*base+c < 1e17 << 2^64`，
uint64 无溢出，无需大数或双模。

## 第二节语义 -> 代码位置 / 测试函数
1. 所有出现(升序含重叠): match/match.go FindAll 主循环; TestFindAllAgainstNaive
2. 无假阳性: match/match.go verify() 哈希命中后逐字符验证; TestRollingHash(碰撞用例)
3. 错误: match/match.go ErrEmptyPattern 哨兵 + 长度判断; TestFindAllAgainstNaive 内 errors.Is
4. 确定性: FindAll 纯函数(仅原子计数器); TestVerifyCountBoundAndConcurrent 并发多次比对
5. 边界(相同->[0]、无出现->空): TestFindAllAgainstNaive 表驱动用例
复杂度上界: match/match.go verifyCount 非导出计数器; TestVerifyCountBoundAndConcurrent
并发 -race 干净: TestVerifyCountBoundAndConcurrent(8 goroutine 并发 FindAll)
