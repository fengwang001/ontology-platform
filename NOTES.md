# Rabin-Karp 推导与交付索引

设窗口哈希为 `H(s)=Σ s[i]*base^(m-1-i) mod mod`。

1. `H(a)=H(b)` 只说明两个窗口落在同一个哈希等价类；有限模数下必然可能有 `a!=b`，
   因此哈希命中是匹配的必要条件，不是充分条件。命中后必须从窗口起点逐字节验证
   `m` 个字符；完全相等才记录位置，否则按碰撞丢弃。
2. 滑窗时先减去离开窗口的最高位：`h' = h - c*base^(m-1)`。整数模运算的数学余数
   非负，但程序中的中间值可能为负。正确更新为先加一个模数：
   `h' = (h - c*pow + mod) % mod`，随后再加新字符并取模。
3. 固定 `base=257, mod=65521, m=3` 时，`"aco"` 与 `"baa"` 哈希均为 11249：
   差值 `1*257^2-2*257-14=65521`，恰好被模数吸收。

## 语义与测试索引

- 所有出现（含重叠）：`check.Naive`；测试 `TestFindAllTable`。
- 无假阳性与碰撞回退：`match.FindAll`；测试 `TestCollision`。
- 空 pattern 错误：`match.ErrEmptyPattern`；测试 `TestFindAllTable`。
- pattern 长于 text：`match.FindAll`；测试 `TestFindAllTable`。
- 确定性：`match.FindAll`；测试 `TestDeterministic`。
- 相同串、无匹配边界：`match.FindAll`；测试 `TestFindAllTable`。
- 验证次数上界：`match.FindAllStats`；测试 `TestVerificationBound`。
