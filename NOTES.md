# NOTES

八步（外层键 "o"；tomb=0 表示无墓碑；inner 记 (ts,rep,val)）：

| 步 | 操作 | tomb | inner | 可见视图 |
|---|---|---|---|---|
| 1 | A Put k1=100@1 | 0 | k1 | k1=100 |
| 2 | A Put k2=200@2 | 0 | k1,k2 | k1=100,k2=200 |
| 3 | A DelOuter@3 | 3 | k1,k2 不清空 | ∅，"o" 不出现 |
| 4 | B Put k3=300@1 | 0 | k3 | k3=300 |
| 5 | B Put k4=400@2 | 0 | k3,k4 | k3=300,k4=400 |
| 6 | Merge(A,B) | max(3,0)=3 | k1..k4 并集 | ∅（ts 全 ≤3） |
| 7 | C Put k5=500@3 | 3 | k1..k4（迟到写不写入） | ∅（3>3 为假） |
| 8 | D Put k6=600@4 | 3 | k1..k4,k6 | k6=600 |

- 甲：Merge 后 tomb=3，"o" 消失。若合并漏传墓碑（当 0），k1=100,k2=200,k3=300,k4=400 全部错复活。
- 乙：k=100（ts 5>2）；按副本名取胜会错取 B 的 999（B>A；按到达顺序同样错）。
- 丙：ts=3 恰等于 tomb 被覆盖；ts=4>tomb 可见 k6=600。若判成 >=：第 7 步后错见 k5=500（正确为 "o" 不出现）。

不变量（代码位置 / 钉住的测试函数）：

1. 与批量重算一致：lww 的 Put/DelOuter/Merge 只做「墓碑取 max + 键级取大」（lww/lww.go Put/DelOuter/Merge），测试用独立 recompute 收集全部操作重算（api/api_test.go）/ TestBatchRecomputeConsistency（SelfCheck 另在 api/api.go 重放八步）
2. 墓碑传播：omap.Merge 对每个外层键调 lww.Merge，两侧墓碑取 max（omap/omap.go、lww/lww.go Merge）/ TestTombstonePropagation
3. 键级 LWW 收敛：lww.wins（ts 大者胜，平局 rep 字典序大者胜，lww/lww.go）/ TestLWWConvergence（双向合并）
4. 失败不留痕：omap.Apply 先 validate 后碰状态，三个互不相同哨兵错误（omap/omap.go）/ TestRejectedOpsLeaveNoTrace

复杂度：omap.OMap.checked 为非导出字段，哈希定位单次 Apply 恒为 1 / TestOuterKeyLookupIsConstantTime；并发只读 api.View 返回全新深拷贝 / TestConcurrentViewsIdentical。
