# NOTES：有界重排窗口保序并行映射器

## 第三节推导：多个输入失败时返回哪个错误

- 锚点：第 1 条要求全部成功时结果与「顺序执行的朴素参照」逐条相同；第 3 条要求失败语义与时序无关。
- 顺序执行参照在第一个失败下标处停下并返回该错误——即「下标最小」的失败，而不是「时间上最早」的失败。
- 因此并行版唯一自洽的选择：返回**失败下标最小**的那个错误（包装带下标），emit 恰好覆盖 `[0, 最小失败下标)`。
- 若改为「返回第一个到达的错误」，返回值会随调度时序漂移（下标 5 先失败就报 5），违反与时序无关；且 emit 会丢掉 3、4 这两个本应产出的有序结果，违反第 1 条的顺序语义。
- 推论：收到第一个错误后**不能立刻停止等待**——下标更小、仍在跑的输入还可能（更晚）失败，必须等全部在跑任务结束，取最小失败下标。
- 取消语义：首个失败到达即 cancel，将其余在跑任务的 `ctx.Err()` 类返回视为「被取消」丢弃（不记为失败、不产生结果），只统计「真实失败」。
- 钉住测试：`TestFailDeterministic`（下标 5 先败、3 后败，循环 200 次，断言返回 3 且 emit 恰为 0,1,2；内联「先到先返」的错误实现断言它会返回 5）。

## 第二节语义 → 代码位置 / 测试

- 1 保序：`pmap/pmap.go` 单调度循环 + `window/window.go` PopReady 连续放行；`TestOrder`（n∈{1,4,16}×w∈{1,4,64} 全组合，与 `check.Naive` 逐条相同）。
- 2 窗口上界：`pmap/pmap.go` 派发闸门 `inflight < n && started-win.Next() < w`（故暂存 ≤ w）；`window/window.go` 非导出计数器 max；`TestOrder` 断言 peak≤n 且 `pmap.LastMaxBuffered()≤w`。
- 3 失败语义：`pmap/pmap.go` 仅保留最小下标的真实失败、首个失败即 cancel、循环等到 inflight 归零再返回；`TestFailDeterministic`。
- 4 错误：`pmap.ErrBadConfig`、`fmt.Errorf("pmap: 输入 %d: %w")` 包装、末尾 `return ctx.Err()`；`TestErrors`（`errors.Is` 三分类）。
- 5 资源：每个 worker 的结果必被接收后才退出，无后台 goroutine；`TestFailDeterministic` 末尾断言 NumGoroutine 回基线。

## 第四节实测

- n=8、w=4、10000 条、耗时伪随机（`i*7919%47` µs）：window 历史最大暂存数实测 **= 4**（≤ W 成立），由 `TestOrder` 的 `{8,4,10000}` 用例断言。
