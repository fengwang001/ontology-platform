# NOTES

## 第三节推导:安全性检查为什么可以贪心

- 定义:状态安全,当且仅当存在一个完成序列,使每个作业轮到它时都满足「剩余需求 <= 当前可用量」,完成后归还全部已分配资源。
- 引理(单调性):若作业 p 在可用量 work 下可完成(need_p <= work),让它完成后 work' = work + alloc_p,逐分量有 work' >= work,即可用量只增不减。
- 推论:完成任意一个可完成的 p,不会把别的可完成作业 q 变成不可完成(need_q <= work 蕴含 need_q <= work');也不会弄丢任何完成序列——若某序列以 q 开头而 p 也可完成,把 p 提到 q 之前仍合法,因为 q 面对的是一个更大的可用量。
- 结论:每轮任挑一个可完成作业假设其完成即可。若某轮没有任何作业可完成,则任何顺序都不可能完成(否则该假想序列的第一个作业此刻就可完成,矛盾)。所以无需回溯枚举,逐轮扫描、每轮至少完成一个,结论与枚举全部顺序一致。
- 反例(total=(6,6)):P0 max(3,3) alloc(2,1);P1 max(3,3) alloc(1,2);P2 max(4,4) alloc(1,1);avail=(2,2)。P2 申请 (1,1) 满足「申请量 <= 当前可用量」,但批准后 avail=(1,1),P0 还需 (1,2)、P1 还需 (2,1)、P2 还需 (2,2),没有任何作业能完成,状态不安全。对应测试 TestUnsafeRequestRejected,其中内联了「只查可用量」的错误实现并断言它会错误批准。

## 第二节语义:代码位置与测试

1. 不超声明、不超总量:bank/bank.go Request 只提交试算安全的状态,Release/Finish 只增 avail;测试 TestConcurrentSafe(每次成功操作后 check.Consistent 断言)。
2. 只批准安全申请且零副作用:bank/bank.go Request 在 maps.Clone 的副本上试算,不安全直接返回 ErrUnsafe 不写回;测试 TestErrorsAndUnsafe 的 unsafe 用例与末尾状态不变断言。
3. 与朴素参照一致:check/check.go Safe 枚举全部完成顺序;测试 TestMatchesNaiveReference(1000 个随机状态,作业数 <= 6,逐一对比)。
4. Release 超量返回 ErrOverRelease 且零副作用、Finish 归还全部:bank/bank.go Release/Finish;测试 TestErrorsAndUnsafe 的 over-release 用例、TestConcurrentSafe。
5. 哨兵错误(ErrUnknown/ErrExceedsClaim/ErrOverRelease/ErrFull/ErrUnsafe):bank/bank.go 顶部 var 声明,均可 errors.Is 区分;测试 TestErrorsAndUnsafe 表驱动用例。

## 第四节复杂度实测

n=50、m=3:上界 n(n+1)/2=1275,实测比较次数 50(一轮扫描全部完成);断言在 TestMatchesNaiveReference 末尾(50 <= cmp <= 1275)。safe 逐轮扫描、每轮至少完成一个作业、不回溯,故总比较次数 <= n+(n-1)+...+1。
