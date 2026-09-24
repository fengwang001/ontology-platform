# 跨重启无空洞序列号分配器 NOTES

## 八步推导（正确实现：先持久化后发放，checkpoint 存「下一个要分配的编号」）

| 步 | 操作 | 返回值 | 操作后内存 next | 操作后 checkpoint |
|----|---------------|--------|------|-------------|
| 1 | Next()        | 0      | 1    | 1           |
| 2 | Next()        | 1      | 2    | 2           |
| 3 | Next()        | 2      | 3    | 3           |
| 4 | Next()        | 3      | 4    | 4           |
| 5 | SimulateCrash | —      | 丢失 | 4           |
| 6 | Recover       | —      | 4    | 4           |
| 7 | Next()        | 4      | 5    | 5           |
| 8 | Next()        | 5      | 6    | 6           |

- (甲) 惰性持久化（每 3 次 Next 才 flush）：第 4 步后 checkpoint 只到 3，崩溃恢复后 next=3，第 7 步会**重复发放 3**（第 4 步已发过），违反「不重复」。
- (乙) Recover 误写 `next = checkpoint + 1`：恢复后 next=5，第 7 步发放 5，**跳过 4**，造成空洞。
- (丙) 读 next→写 next+1 无锁：两个 goroutine 同读 next=0、各写 1，**都拿到 0**（重复）；两次调用只发出 {0,0}，**编号 1 被跳过**（本应发 {0,1}）。

## 四条不变量落点

1. **不重复**：`seq/seq.go` 的 `Next` 先持久化后发放，checkpoint 是提交点，崩溃恢复只可能重发「未提交」的号（不存在）；钉于 `TestCrashRecoverNoDupNoHole`。
2. **不空洞**：第 k 次成功 `Next` 返回 k-1（内存 next 单调 +1），`Recover` 直接取 checkpoint 为 next 不加不减；钉于 `TestEightStepSequence`。
3. **与朴素参照一致**：恢复后 next == 崩溃前已持久化的 next；钉于 `TestCrashRecoverNoDupNoHole`、`TestRecoverRecordCountConstant`。
4. **失败不留痕**：`api.New` 先校验 dir 再动文件系统；`seq.Next` 持久化失败提前返回、内存 next 不动；`check.Read` 校验通过前不写任何状态；钉于 `TestFailureLeavesNoTrace`、`TestCorruptCheckpoint`、`TestMisc`。
