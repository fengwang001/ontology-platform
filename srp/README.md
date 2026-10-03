# srp — 多单元资源的栈资源策略（SRP）启动闸门

`package srp` 实现一个可并发调用的 Stack Resource Policy 闸门：抢占层级按
相对截止期动态推出，资源天花板按余量动态推出，作业的**唯一准入点**是
`Start`；一旦压栈，取放资源只需栈顶判定。

## 接口

| 调用 | 语义 |
| --- | --- |
| `New() *SRP` | 创建空闸门 |
| `DeclareResource(id, N)` | 声明资源，`N∈[1,1000]`，`avail=N`，至多 8 个 |
| `AddTask(id, D, mus...)` | 增加任务，`D∈[1,10^6]`，至多 4 项 `Mu{Resource,Units}`，未列出的资源 μ=0 |
| `RemoveTask(id)` | 删除任务；仍有未结束作业时拒绝 |
| `Start(jobID, taskID)` | **启动闸门**：通过层级与天花板检查后压栈，运行中作业至多 32 个 |
| `Acquire(jobID, r, u)` / `Release(jobID, r, u)` | 仅栈顶；`u∈[1,1000]` |
| `Finish(jobID)` | 仅栈顶且不持有任何资源，随后弹栈 |
| `Ceil(r)` / `SysCeil()` / `Avail(r)` / `Level(t)` / `Stack()` | 当前状态的纯查询 |

拒绝一律返回 `*srp.Error`，其 `Reason` 字段是稳定原因码（见 `errors.go`），
按以下顺序**只报第一个**：
`invalid_argument` → `not_found` → `duplicate` → `capacity_exceeded` →
`not_top_of_stack` → `preemption_level_low` → `ceiling_blocked` →
`over_claim` → `unit_unavailable` → `not_held` → `still_holding` →
`task_in_use`。被拒操作不改变 `avail`、栈、持有量与 π。

## 抢占层级 π 的推法

把**当前全部任务**的互不相同 D 值从大到小排序：D 最大者 π=1，其次 π=2，
依此类推；D 相同则 π 相同。任务增删后 π 自动重排（中间插入新 D 时其余
任务顺延），已入栈作业的栈次序在任何时刻仍保持严格递增。

## 天花板与严格不等号

```
Ceil(r)  = max{ π_k : μ_{k,r} > avail_r }   // 无任务满足则 0
SysCeil   = max_r Ceil(r)                    // 无资源则 0
```

关键是**严格大于**：μ 恰等于 `avail` 不计入天花板，必须 μ = avail+1 才计入；
多资源时 `SysCeil` 取各资源天花板的最大值。`Acquire` 使余量下降、天花板
上升，`Release` 归还后天花板随之下降。

## 栈次序规则

`Start` 的两个闸门（栈空时跳过第一个）：

1. 任务 π **严格大于**栈顶作业所属任务的 π；
2. 任务 π **严格大于**当前 `SysCeil`。

因此任意时刻自栈底到栈顶，作业所属任务的 π 严格递增；嵌套作业只能在更
高层级之上。`Acquire/Release/Finish` 都要求调用者是栈顶，`Finish` 还要
求持有量为 0。

在“每个作业都遵守自身 μ 声明”的操作序列下，`u > avail` 的单元不足永不
发生（标准 SRP 嵌套余量论证），实现内部用非导出计数器
`shortageRejections` 断言其恒为 0，并在对拍中与朴素模拟比对。

## 不变量

- 任意资源：`avail + 全部作业持有量之和 == N`；
- 栈内作业 π 自栈底向栈顶严格递增；
- 所有公开方法在同一把互斥锁下完成“检查 + 提交”，并发调用等价于某个
  串行顺序；相同操作序列重放得到完全相同的结果。

## 本地验证

```bash
# 全量测试（定点规则 + 2000 组对拍 + 并发）
go test ./...

# 竞态检测
go test -race ./...

# 查看逐步输入/输出/判定依据日志（前 3 组对拍序列全量打印）
go test -run TestDifferentialNaive2000 -v ./srp
go test -run TestWalkthrough -v ./srp
```

`srp_test.go` 覆盖：μ 恰等于 avail 不入天花板而大 1 入、相同 D 不能互相
抢占、中间 D 插入后 π 顺延、多资源取最大、归还后天花板下降、未列出资源
μ=0、非栈顶 Release/Finish 拒绝；`naive_test.go` 是独立逐字照抄规则的朴素
模拟，`differential_test.go` 用 2000 组随机合法/非法序列逐步对拍操作结果
与全量查询快照（`Ceil/SysCeil/Avail/Level/Stack`）。
