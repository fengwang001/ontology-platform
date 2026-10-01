# 分布式追踪跨度组装器（`tracing` 包）

把乱序到达的跨度组装成调用树，校正跨服务时钟偏移，并在追踪完结后输出关键路径。
输出只取决于跨度集合本身，与到达顺序无关；未完结追踪的暂存跨度总数受上限约束。

## 跨度模型与投递

跨度 `Span` 含追踪号 `TraceID`、跨度号 `SpanID`、父跨度号 `ParentID`（根为空）、
服务名 `Service` 与起止时刻 `Start`/`End`。通过 `Assembler.Submit` 投递，
`Submit` 与 `Poll`（时钟推进检查）均可被并发调用（内部互斥）。

以下跨度被拒绝并给出可区分的原因（`RejectReason`），被拒绝的跨度不改变任何状态：

- `end-before-start`：结束早于开始；
- `self-parent`：以自己为父；
- `duplicate-root`：同一追踪出现第二个根；
- `conflicting-duplicate`：同一跨度号内容不同的重复；
- `trace-completed`：追踪已完结后迟到（单独计数 `Stats.LateRejected`）；
- `buffer-full`：接受后会使未完结追踪的跨度总数超过 `MaxBufferedSpans`。

内容相同的重复为幂等：返回 `Duplicate=true`，不改变任何状态。

## 组装与完结规则

- 每个追踪记录最近一次接受跨度的时刻；根已到达且静默满 `SilenceTimeout`
  （以注入时钟 `Clock` 计）即完结并释放，`Poll` 返回 `TraceResult`。
- 完结时沿父链向上不能到达根的跨度列为孤儿：父跨度不存在，或父子关系成环
  （含挂在环上的后代）。孤儿不参与平移与关键路径。
- 不变量：已接受跨度数 == 已完结追踪释放的跨度数（树中 + 孤儿）
  + 未完结追踪暂存的跨度数，即 `AcceptedTotal == CompletedSpans + BufferedSpans`。

## 偏移校正与重判顺序

自根向下逐层进行。对每条父子边，若子与父服务不同且子区间不在父（已平移后的）
区间内，则把子跨度连同其子树中不经过其他服务的同服务后代整体平移：

- 平移量取使子区间落入父区间的最小绝对值（整体早于父则右移 `父.Start-子.Start`，
  整体晚于父则左移 `父.End-子.End`）；
- 子比父长时改为起点对齐（`delta = 父.Start - 子.Start`）。

同服务后代随组平移；异服务后代不在本组内，而是相对已平移的父在下一层重新判定，
平移量沿路径累计。输出中每个树中跨度带累计平移量 `PlacedSpan.Shift`。

## 关键路径

从根起，每层在子跨度中选（平移后）结束时刻最晚者，并列时取跨度号最小者，
直至叶子，输出跨度号序列 `TraceResult.CriticalPath`。

## 本地验证

```bash
# 全部测试（含全排列到达的输出对比、并发与不变量检查）
go test ./tracing/

# 竞态检测 + 详细日志（日志打印输入、输出与平移/孤儿/拒绝的判定依据）
go test -race -v ./tracing/
```
