# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## Spot 回收预警下的任务排空与重调度器

实现在 `scheduler` 包（`scheduler/scheduler.go`）。用 `New(G, Pod, Bud)` 构造：

- `G`：回收宽限期，1..10^6；截止时间 `dl = now + G`。
- `Pod`：按需单价，1..10^6。
- `Bud`：按需预算，0..10^12，只减不增且永不为负。

任一构造参数越界即整体拒绝（`ErrInvalidConfig`），不产生任何对象。

### 任务与节点

- `AddNode(id, kind, slots)`：`kind` 为 `Spot` 或 `OnDemand`，`slots` 1..1000。
- `AddTask(id, prio, w, iv, ck)`：`prio` 0..255；`w`（总工作量）、`iv`（检查点间隔）、
  `ck`（单次落检查点耗时）均在 1..10^9。新任务为待放置，`p=cp=rs=0`。
- 任务恒处三态之一：`Pending`（待放置）、`Running`、`Done`。

### 放置规则 `Place()`

待放置任务按 `(prio 降序, id 升序)` 逐个处理：

1. `rs >= 2` 的任务没有 Spot 候选，直接进入下一步。
2. 否则在「未被通知且有空闲槽」的 Spot 节点中，取空闲槽最多者；并列取 id 最小者。
   Spot 放置免费。
3. 无 Spot 候选时，在有空闲槽的 OnDemand 节点中取 id 最小者；成本
   `(w - cp) × Pod` 必须不超过剩余预算，成功才放置并扣减预算。
4. 预算不足或无槽则该任务继续待放置，不阻塞后续任务（本轮可再次 `Place()` 重试）。

### 进度与检查点 `Report(id, p')`

任务须处于 `Running` 且 `p <= p' <= w`。更新 `p = p'`，检查点
`cp = max(cp, ⌊p'/iv⌋ × iv)`，即检查点只增不减；`p' = w` 时任务完成并释放槽位。

### 排空计划 `Notice(n, now)`

`n` 必须是未被通知的 Spot 节点。对其上每个运行任务分类：

- **自然完成**：剩余 `w - p <= G`（取等归入），不主动处理，等其上报完成。
- **已保存（不耗时）**：未保存量 `u = p - cp` 为 0，直接记入已保存，不参与累计排序。
- **需落检查点**：`u > 0` 且非自然完成。按 `u 降序、id 升序` 排列，逐个累加耗时
  `ck`：累计值 `<= G` 者保存（令 `cp = p`）；遇到第一个使累计值 `> G` 的任务立即
  停止，**它与其后全部任务记为丢弃**，不跳过尝试更小的任务。

返回的完成、保存、丢弃三个列表均按 id 升序。节点此后永久退出放置候选（直到
`Expire` 移除；同 id 可在移除后重新添加）。

### 到期移除与返工 `Expire(n, now)`

要求节点已通知且 `now >= dl`。节点被移除；其上**仍未完成**的任务（包括被归入
自然完成却尚未上报完成的任务）回到待放置，`rs++`、`p = cp`。每个任务的返工量
定义为回退前的 `p - cp`，按 id 升序返回；所有 `Expire` 返回返工量之和等于累计
返工。被丢弃的任务因 `cp` 未推进，其未保存进度全部计入返工。

### 并发与确定性

所有方法在单个互斥锁下串行化，可并发调用且结果等价于某一合法串行顺序；
任意时刻槽位占用不超过 `slots`、预算非负、Spot 上任务 `rs <= 1`、`p >= cp`。
全部决策仅依赖显式输入，无随机量，相同操作序列重放得到完全相同的排空计划、
返工量与放置结果。

### 错误判定顺序

- `AddNode` / `AddTask`：先「参数非法」，再「已存在」。
- `Notice`：参数非法（含 `now < 0`）→ 节点不存在 → 非 Spot → 已通知。
- `Expire`：参数非法 → 节点不存在 → 未通知 → 未到期（`now < dl`）。
- `Report`：任务不存在 → 未运行 → 进度非法。

被拒绝的操作不改变任何节点、任务、预算与槽位。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

针对调度器：

```bash
# 全部用例（含 2000 组随机序列对朴素模型的差分对照）
go test ./scheduler -count=1 -v

# 竞态检测（含并发混合调用压测）
go test -race ./scheduler -count=1
```

`scheduler/scheduler_test.go` 覆盖题目要求的全部边界：剩余恰等于 G 归自然完成、
`u=0` 不占序、累计恰等于 G 保存而大 1 即停止、停止后更小任务一并丢弃、u 并列按
id、检查点 floor 且单调、Expire 回退与返工、自然完成未上报也回退、Spot 候选并列
取小 id、已通知节点不接收放置、按需成本与预算不阻塞后续、`rs>=2` 只放
OnDemand、被拒绝不改状态。

`scheduler/diff_test.go` 内置按题面规则逐步实现的朴素参考模型，对 2000 组随机
节点 / 任务 / 事件序列逐步比对错误码、`Notice` / `Expire` 输出及完整状态快照，
失败时打印每组输入、输出与判定依据（`-v` 时前 5 组打印完整轨迹）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
