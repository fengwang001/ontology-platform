# NGE 笔记（单调栈 / 下一个更大元素）

## 推导：栈顶等于新元素时弹不弹（序列 [2,2,3]，栈内为下标，右端为栈顶）

做法 A：相等也弹（弹栈条件 a[top] <= x）
  i=0: 栈空，压 0                     栈 [0]    答案 [-,-,-]
  i=1: a[0]=2 <= 2，弹 0→ans[0]=1，压 1  栈 [1]    答案 [1,-,-]
  i=2: a[1]=2 < 3，弹 1→ans[1]=2，压 2   栈 [2]    答案 [1,2,-1]
  收尾：栈内剩余补 -1 → 完整答案 [1,2,-1]，语义为「下一个大于等于」

做法 B：相等不弹（弹栈条件 a[top] < x）
  i=0: 栈空，压 0                     栈 [0]    答案 [-,-,-]
  i=1: a[0]=2 < 2 不成立，不弹，压 1    栈 [0,1]  答案 [-,-,-]
  i=2: 弹 1→ans[1]=2；弹 0→ans[0]=2；压 2  栈 [2]  答案 [2,2,-1]
  收尾 → 完整答案 [2,2,-1]，语义为「下一个严格更大」

差异在下标 0：A 指向 1（等值即算满足），B 指向 2（跳过等值取严格更大）。
本题采用做法 B「下一个严格更大」，弹栈条件为 a[top] < x，见 nge.NextGreater 文档注释。

## 四条不变量：保障位置 + 钉住它的测试

1. 结果正确：mono.Scan 弹出栈顶时立即定答案（mono/mono.go 的 Scan 主循环）；
   TestNextGreaterAgainstNaive 与朴素双循环逐元素对照，TestSelfCheckDetectsTampering 反向钉住。
2. 栈单调：弹栈条件 a[top] < x 保证栈内值自底向顶非递增（mono/mono.go 的 Scan）；
   TestStackMonotonic 逐步重放扫描，断言每次压入前栈顶值 >= 新值。
3. 无解可判定：答案槽位先全部置 -1（mono/mono.go 的 Scan 开头），-1 与任何合法下标（含 0）不混淆；
   TestNextGreaterAgainstNaive 的全相等用例、TestSelfCheckDetectsTampering 的 false none 用例钉住。
4. 失败不留痕：Scan 先做 nil/超限校验再扫描，拒绝时不碰输入、不返半截（mono/mono.go 的 Scan 开头）；
   TestRejections 钉住，并验证被拒绝后扫描器仍可正常扫描。
