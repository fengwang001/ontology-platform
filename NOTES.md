## `[2,2,3]` 推导

采用右侧第一个“严格更大”：扫描到 `a[i]` 时，仅当 `a[top] < a[i]` 才弹出并把答案定为 `i`；相等不弹。

| 下标 | 严格更大：弹出/栈内容/已定答案 | 大于等于：弹出/栈内容/已定答案 |
|---|---|---|
| 0 | 无；`[0]`；`[]` | 无；`[0]`；`[]` |
| 1 | 相等不弹；`[0,1]`；`[]` | 弹 0；`[1]`；`ans[0]=1` |
| 2 | 弹 1、弹 0；`[2]`；`ans[0]=2,ans[1]=2` | 弹 1；`[2]`；`ans[0]=1,ans[1]=2` |

严格更大答案：`[2,2,-1]`；大于等于答案：`[1,2,-1]`。差异在下标 0：相等元素 1 不是严格更大，所以严格语义跳过 1、指向 2。

## 不变量与保证

1. 结果正确：`mono.Scanner.scan` 只在 `a[top] < a[i]` 时定右侧最近答案；`nge.SelfCheck` 逐位复核，测试 `TestSelfCheckMatchesNaive` 钉住。
2. 栈单调：栈内下标对应值自底向上非递减；`mono.Scanner.scan` 弹到可压栈为止，测试 `TestMonotonicStackTrace` 钉住。
3. 无解可判定：无解保留 `nge.NoAnswer = -1`，不会和合法下标 0 混淆；`nge.SelfCheck` 检查其右侧无严格更大值，测试 `TestNoAnswerDoesNotClashWithZero` 钉住。
4. 失败不留痕：`nge.NextGreater` 与 `nge.NewScanner` 在扫描前校验 nil、长度上限和非法上限；拒绝时不建结果且扫描器可复用，测试 `TestRejectedOperationsLeaveNoTrace` 钉住。
