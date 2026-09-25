# Rabin-Karp 滚动哈希笔记

## 碰撞回退与取模

更新规则 `h=(h*base+c)%mod` 把无穷字符串映射到 `[0,mod)` 的有限整数，
抽屉原理决定不同子串必有哈希相等（碰撞），故“窗口哈希==pattern 哈希”
只是必要条件。哈希命中后必须回退该窗口逐字符验证，通过才记录位置，
碰撞窗口被否决不产生假匹配。

长度 m 的窗口右移一位需减去离开的最高位：`h - c*base^(m-1)`，结果可能
为负；Go 对负数取模得负值会使哈希长期错乱。正确做法先加回 mod：
`(h - c*pow + mod) % mod`（uint64 下写成 h+mod-term 防下溢）。

实现取 base=911371、mod=1000003（互素大质数，正常输入碰撞极少）。
确定性碰撞：3 字符 "   " 与 "\"9o" 哈希同为 615248。
Slide: h'=((h-c*pow+mod)%mod*base+newc)%mod，pow=base^(m-1)%mod。

## 代码位置与测试（实现完成后回填）

- 滚动原语：hash/hash.go 的 `Append`/`Slide`/`Value`、哨兵错误。
- 匹配与验证：match/match.go 的 `FindAll`、`verifyAt`、`VerifyCount`。
- 朴素/错误参照：check/check.go 的 `NaiveFindAll`、`BuggyFindAll`。
- 全部出现：TestFindAll；空 pattern：TestEmptyPattern；
  pattern 过长：TestPatternLongerThanText；确定性：TestDeterminism；
  碰撞无假阳性+错误实现假匹配：TestCollisionNoFalsePositive；
  验证次数上界：TestVerifyBound；并发 -race：TestConcurrent。
