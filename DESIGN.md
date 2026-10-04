# 流水线调度器设计说明

## 结构
- `group/group.go`：组表。每组一个状态 `{placeholder, pending int64}`；`map[string]*State`
  保存占位列与 Pending 列，终态后双双归零时删除表项。提供 `Get/Set/Ensure/ClearIfEmpty`，
  不感知运行状态与队列，逻辑全由 sched 编排。
- `slot/slot.go`：执行位管理器。`running int` 计数（Running 与 Cancelling 各占一）；
  等待队列用 `container/list`，队尾 `PushBack(id)`、队首 `PopFront` 均 O(1)；
  取消队列中部的 Waiting 通过记录上的指针标记，`Remove` 由 `list.Element` O(1) 删除，
  不做线性扫描。队列里存的是 int64 运行编号，次序即“成为占位者的时刻”。
- `sched/sched.go`：生命周期编排。单把 `sync.RWMutex`（写锁串行化全部状态变更）即满足
  “可并发、结果等价于某串行顺序”；读方法用 RLock。被放弃方案：分片锁/无锁队列——规则中
  Submit 同时触达组、运行、队列、执行位，分片锁升级路径复杂，收益仅在极端争用，不值当。

## 关键取舍
- Cancelling 继续占执行位直到终态（AckCancel 或迟到 Finish）：取消只是请求，执行位必须
  由占位者终态释放，否则“取消中的运行何时让出位置”不可精确复现。
- Pending 晋升走队尾，不继承原提交次序：规则要求次序按“成为占位者的时刻”，晋升才成为
  占位者；因此示例中早提交的 r4 排在 r5 之后。
- 队列已满用“净增判定”解析模拟，不真实改状态：新占位入队后会被分配阶段连续出队
  `k = min(Q0, C-S0)` 个，仅当 `k == Q0`（分配后仍有占执行位者）且原队列长 == Q 才拒；
  Pending 路径与顶掉 Waiting（净增 0）永不被拒，符合“先判定后变更”。
- Pending 晋升可超 Q：晋升不受 Q 约束，`Enqueue` 不设上限，Q 只在 Submit 净增判定中使用。

## touched 证明
- 每次操作开始递增 `touchEpoch`，`touch(id)` 用 `r.seen != epoch` 统计唯一被读写的运行。
- Submit 至多：旧 pending + 占位者 + 新运行 + 至多 1 个被分配出队者 = 4；
  Finish/AckCancel 至多：目标 + 本组晋升者 + 至多 1 个出队者 = 3；
  Cancel 至多 2。分配循环每轮只触达队首一个运行，与组数、队列长度无关；
  队列中部取消靠 `list.Element` O(1) 移除。

## 错误
- 四类哨兵错误 `ErrInvalidArgument/ErrNotFound/ErrConflict/ErrQueueFull`，
  操作返回 `fmt.Errorf("...: %w", sentinel)`，`errors.Is` 可区分。

## 本地验证
- `go test ./... -race -v`：表驱动确定场景 + 与朴素参考模型逐条对照 1500 组随机序列
  （另跑两档队列长度 100/10000 的 touched 对照与并发不变量）；日志打印每步输入、输出与
  参考模型判定依据；`gofmt -l .`、`go vet ./...`。
