# Treiber stack — NOTES

New(3) 八步（内容按 栈顶→栈底）：

| 步 | 操作 | Len | 栈内容 | 返回值/错误 |
|---|---|---|---|---|
| 1 | Push(1) | 1 | [1] | nil |
| 2 | Push(2) | 2 | [2,1] | nil |
| 3 | Push(3) | 3 | [3,2,1] | nil |
| 4 | Push(4) | 3 | [3,2,1] | ErrFull（状态不变） |
| 5 | Pop | 2 | [2,1] | 3, true |
| 6 | Pop | 1 | [1] | 2, true |
| 7 | Pop | 0 | [] | 1, true |
| 8 | Pop | 0 | [] | 0, false（空，状态不变） |

甲：5/6/7 依次返回 3,2,1（LIFO）。若 Pop 错做成栈底出（FIFO），三次依次返回 1,2,3；第 5 步即分叉（正确 3 vs 错误 1）。
乙：入栈值取 0：Push(0) 后 Pop 得 0，空栈 Pop 也得 0；无 ok 时下游 `if v := st.Pop(); v == 0 { 当作空 }` 会把真实元素 0 误判成空而漏处理。
丙：A 非原子把栈顶直接写成 1 后栈为 [1]：B 刚压入的 3 丢失（节点 2 也被一并孤立）；正确 CAS 时 A 的 CAS(2→1) 因栈顶已变 3 而失败，重试读 3、后继 2，CAS 成功返回 3，栈变 [2,1]，无丢失。

不变量 → 代码保证位置 → 钉住的测试函数：

- I1 与朴素 Mutex+切片栈逐次一致：s/stack.go 的 Push/Pop CAS 循环；s/selfcheck.go 的 SelfCheck+refStack 随机交错对照；TestEquivNaive（多种子表驱动）。
- I2 LIFO 且 Len=入-出、恒在 [0,max]：Pop 返回 top.V、top CAS 到 top.Next()，pushed/popped 两个原子计数；TestEightSteps、TestLIFOConservation。
- I3 无丢失无重复：只有 CAS 成功才 pushed.Add(1)/popped.Add(1)，失败必重试；TestConcurrentN（N 档表驱动，集合全等、每个值恰 1 次）。
- I4 失败不留痕：ErrInvalidMaxLen/ErrFull/ErrClosed 三个哨兵互不相同，且都在任何状态写入前 return；TestRejectedNoTrace、TestCloseDrain。
复杂度：非导出字段 lastPopVisited 记录最近一次 Pop 访问节点数，s 包内测试直接读取断言恒为 1：TestPopVisitsOne（m=100/1000/10000）。
