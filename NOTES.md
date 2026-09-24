# NOTES
## 九行分步表（步 | i指向键 | j指向键 | 是否比较 | 结果 | 输出 | 累计比较次数）
1 | 1 | 2 | 是 | 旧<新 | D(1) | 1
2 | 3 | 2 | 是 | 旧>新 | I(2) | 2
3 | 3 | 3 | 是 | 键相等、值相同(b=b) | 无 | 3
4 | 4 | 4 | 是 | 键相等、值不同(c≠C) | U(4) c→C | 4
5 | 7 | 5 | 是 | 旧>新 | I(5) | 5
6 | 7 | 9 | 是 | 旧<新 | D(7) | 6
7 | 9 | 9 | 是 | 键相等、值相同(e=e) | 无 | 7
8 | — | 10 | 否（旧侧已耗尽，新尾） | 不比较 | I(10) | 7
9 | — | 12 | 否（旧侧已耗尽，新尾） | 不比较 | I(12) | 7
（甲）共 7 条：D1、I2、U4、I5、D7、I10、I12，按键升序。漏尾部则少 I10、I12，重放结果止于 (9,e)，缺键 10/12。同值也输出 U 则多 U(3,b→b)、U(9,e→e)。
（乙）多 (4,D)：相邻相等→ErrDuplicateKey；10/9 对调：第一处违规是 9<10→ErrNotSorted。不校验直接归并则在步 7 把 9<10 判成 D9，旧侧耗尽后尾部再 I10、I9、I12：相对正确差分多出 D9、I9（且日志不再严格升序）。
（丙）比较次数恰为 7；「耗尽侧视为 +∞、两侧都耗尽才停」会再比 (∞,10)、(∞,12) 共 9 次；old=1..n、new=n+1..n+m 时每步旧<新连出 n 次 D 后旧侧耗尽，尾部 m 个 I 不比较，故恰为 n。
## 四条不变量 → 代码保证位置 / 钉住的测试函数
1 重放等价：merge.go 双指针输出携带 Old/New，api.go Advance 先在当前快照上算全量结果、全部通过后才提交；TestReplayEquivalence。
2 与朴素参照一致：merge.go 一次三路比较决定 I/D/U，与 map 并集判定同源同序；TestMatchesNaiveReference。
3 最小性 & Key 严格升序：merge.go 键相等且值同只推进指针不输出，每键至多一条；TestMinimalAndSorted。
4 失败不留痕：api.go New/Advance 先校验（ErrMaxChanges/ErrDuplicateKey/ErrNotSorted/ErrTooManyChanges）后改状态，入参防御拷贝，统计仅成功后累加；TestRejectedAdvanceNoStateChange。
