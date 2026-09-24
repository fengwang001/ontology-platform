# CDC Regrouper NOTES

maxRows=3 十三步（R=ROW C=COMMIT X=ROLLBACK；输出/Buf 为步后状态）

| # | 事件 | 判定 | 输出 | Buf | 进行中事务缓冲 |
|-|-|-|-|-|-|
| 1 | BEGIN1 | 接受 | 无 | 0 | 1:[] |
| 2 | BEGIN2 | 接受 | 无 | 0 | 1:[],2:[] |
| 3 | R(2,a) | 接受 | 无 | 1 | 2:[a] |
| 4 | R(1,b) | 接受 | 无 | 2 | 1:[b],2:[a] |
| 5 | BEGIN3 | 接受 | 无 | 2 | 1:[b],2:[a],3:[] |
| 6 | R(3,c) | 接受 | 无 | 3 | 1:[b],2:[a],3:[c] |
| 7 | R(2,d) | 拒绝·缓冲满 | 无 | 3 | 同上（d 未入） |
| 8 | C(2) | 接受 | T2:[a] | 2 | 1:[b],3:[c] |
| 9 | R(1,e) | 接受 | 无 | 3 | 1:[b,e],3:[c] |
| 10 | X(3) | 接受 | 无 | 2 | 1:[b,e] |
| 11 | R(1,f) | 接受 | 无 | 3 | 1:[b,e,f] |
| 12 | C(1) | 接受 | T1:[b,e,f] | 0 | 无 |
| 13 | C(3) | 拒绝·未知事务 | 无 | 0 | 无 |

正确最终输出：[T2(a)]、[T1(b,e,f)]
甲（按 BEGIN 顺序按住）：第 8 步无输出，T2 连 a 被按住继续占账（Buf=3）；第 9 步 e 变缓冲满被拒（正确下接受）；第 11 步 f 接受；第 12 步错成 [T1(b,f)][T2(a)]——顺序颠倒且 T1 丢 e 多 f；13 步仍未知事务。
乙（不缓冲直接透传）：下游依次收 a,b,c,d,e,f；c 属回滚事务 3；d 在正确实现第 7 步即被缓冲满拒绝、永不下发（f 在正确实现第 11 步因 X(3) 腾出空位而被接受）。
丙（ROLLBACK 忘释放）：第 11 步 f 变缓冲满被拒（幽灵 c 占位；正确下接受）；T1 错成 (b,e) 漏 f；全部结束 Buffered() 错为 1（应 0）；第 13 步两种实现均为未知事务错误、无输出（该 bug 只污染总账，不改状态机）。

不变量（代码位置 / 钉住的测试）：
1. 朴素参照一致：regroup.go Commit 按本 tx 缓冲入 output 日志、Output() 逐字段复制；api.go naive 独立复算；TestReferenceEquivalence、TestSelfCheck。
2. 原子性：txn.go Commit 只交回本 tx 全部行并置 committed、Abort 丢行；回滚行不入日志、每 tx 至多一次；TestThirteenSteps、TestRules。
3. 缓冲精确：regroup.go buffered 在 ROW/COMMIT/ROLLBACK 三处等额定额增减，提交/回滚立即释放；TestThirteenSteps、TestCheckedCounterBounds。
4. 失败不留痕：api.go Feed 非法事件先判返回、regroup.go 满额判定先于 append，错误分支不写任何状态；TestRejectionLeavesState、TestInvalidEvents。
