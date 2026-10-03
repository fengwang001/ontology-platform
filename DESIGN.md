# 审批流引擎设计说明

## 结构
- `org`：员工表（`map[string]*employee`），维护上级 `manager` 与审批额度 `limit`；员工名/键用非空 `string`（对应非空字节串）。未出现的员工视为无上级、额度 0。
- `approval`：引擎持有一把 `sync.RWMutex`、`org`、`deadline.Heap` 与申请表；申请保存**提交时冻结**的候选链、当前候选下标 `pos`、分配时刻 `ta`、终局与终局时刻。
- `deadline`：手写最小堆，元素为 `(req, dueAt)`，按 `dueAt` 升序，支持 `Pop`/`Peek`/`Update`/`Delete`/`Push`，供到期处理使用。

## 关键取舍
1. **审批链在 Submit 时冻结，审批资格在 Decide 时按当前 org 实时校验**。冻结保证链稳定、可审计、可重放；实时校验使撤权立即反映为 `ErrRevoked`，资格不滞后。放弃的方案：整体快照（资格滞后于撤权）、整体实时（后续改链会改变候选，不可复现）。
2. **撤权不立即升级，只由超时驱动升级**。`SetManager`/`SetLimit` 不带 `now` 参数；若撤权即刻升级，升级时刻依赖 org 操作的到达时刻，相同操作序列无法重放出相同结果。放弃的方案：撤权即刻升级。org 操作只改当前组织表，不触发任何到期处理，也不改已冻结候选。
3. **升级起点取旧 `ta+T`（到期时刻）而非操作处理时刻 `now`**。到期是由时间与 T 决定的确定事件，连续升级时各级时刻为 `ta+kT`，与操作何时到达无关；`Status(now)` 是 `now` 的纯函数，被拒绝的操作不落任何效果，下次成功操作结果相同。
4. **终局唯一**：终局记录后不再修改。堆中不再有待决项的申请；Expired 与 Approved/Rejected 互斥。

## 到期处理
- 每个成功的带 `now` 操作：先校验 `now>=clock`，对虚拟状态计算本次结论（不改状态），全部校验通过后，驱动堆处理所有 `dueAt<=now` 的堆顶项，再落实本体并推进 `clock=now`。
- 堆顶处理：`dueAt = ta+T`；到期则 `pos++`、`ta += T`；还有候选则更新堆项，否则终局 `Expired`（终局时刻 `ta+T`）并删除堆项。
- 在操作落实前，到期效果仅以纯算术（`k=max(0,floor((now-ta)/T))`）在只读/虚拟视图中体现。

## 错误优先级
参数非法 → `ErrClock` → 申请不存在（Submit 为已存在）→ `ErrClosed` → `ErrNotAssignee` → `ErrRevoked`；Submit 的 `ErrNoApprover` 排在“已存在”之后。

## 本地验证
```bash
go test ./...                      # 全部测试
go test -race -run . ./approval    # 并发竞态
go test -run TestRandomNaive -v ./approval  # 随机序列 vs 逐步朴素模拟
go vet ./... && gofmt -l .
```
