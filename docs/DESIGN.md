# 基数约束并发名额控制 —— 设计说明

## 1. 问题与正确性目标

本体平台的链接类型声明基数上限 `N`。设某作用域（链接类型 + 受约束端对象，
如「部门 dept-1 的 employees」）当前：

- `confirmed`：已确认关联数；
- `inFlight`：已获批但尚未最终确定（成功/失败）的进行中请求数。

幻影式基数绕过的根源是「先读后判」：请求先读 `confirmed`，在锁外判断
`confirmed < N`，再写入。两个请求可同时读到同一个 `confirmed` 而双双获批。

本设计把「读取当前占用 + 判断剩余名额 + 预占名额」合并为一个原子操作：
**准入判定成功的同一临界区内立即占用一个名额**。因此不变量

```
confirmed + inFlight <= N
```

在任何可观测时刻都成立，且准入操作是可线性化的：每个操作在临界区内有唯一
线性化点，整个并发执行等价于按该点排序的某种串行执行。

## 2. 核心数据结构与操作

每个作用域（`ontology/guard.go` 的 `scope`）持有：

- `links map[Link]struct{}`：已确认关联集合，按 `(source, target)` 去重；
- `pending map[requestID]*reservation`：进行中占用；
- `deadlineHeap`：按中断租约到期时刻排序的最小堆，堆顶即最早可能释放者；
- `version`：作用域基线版本，**仅成功提交递增**；
- `outcomes`：每个请求的最终裁定（COMMITTED / ROLLED_BACK / EXPIRED）。

所有作用域共用一把 `Guard.mu`。操作语义：

| 操作 | 临界区内动作 |
| --- | --- |
| `Begin` | 先回收过期占用；按固定优先级判定；获批则入表入堆、占用名额 |
| `Heartbeat` | 延长该请求租约的 deadline，`heap.Fix` |
| `Commit` | 出堆出表，关联不存在则落 `links` 且 `version++`；已存在则按失败释放 |
| `Rollback` | 出堆出表，名额立即释放，`version` 不变 |
| `Reap/Snapshot/Begin` 入口 | 惰性回收所有 `deadline <= now` 的占用并记 EXPIRE |

> 未注册的作用域按容量 0 处理（安全默认：任何新建都被拒为 `CONFIRMED_FULL`，
> 不会意外放行）；正式使用应先 `EnsureScope`。

## 3. 需求逐条对照

### 3.1 进行中请求必须占用名额（防幻影）

`Begin` 的最终判据是 `confirmed + inFlight >= N`，且计数与自增在同一临界区。
任何尚未最终确定的请求，只要它持有 `pending` 表项，就被计入 `inFlight`。

### 3.2 失败释放原子且立即可见；被拒者不自动重试

`Rollback`（以及过期回收）删除占用与下一个 `Begin` 的判定共用同一把锁，
释放对其后的第一个请求必然可见，不存在「已释放但仍读旧值」的窗口。
本设计**不做条件变量排队**：被拒请求不会被自动唤醒，必须由调用方重新发起；
新到达请求则天然看到新状态。

### 3.3 三类拒绝原因互斥且判定顺序固定

`Begin` 中的判定顺序为：

1. `BASELINE_CONFLICT`：`observedVersion != version`，最高优先级；
2. `DUPLICATE`：关联已存在（不重复计数）；
3. `CONFIRMED_FULL`：`confirmed >= N`；
4. `IN_FLIGHT`：`confirmed < N` 但 `confirmed + inFlight >= N`，暂时性。

基线冲突先于任何名额判定；已确认满额先于进行中占用判定。

### 3.4 最后一个名额的严格争夺：恰一成一败

计数与占用是同一个原子操作。`TestLastSlotContention` 用 64 个 goroutine
在同一关闭的 channel 上同时冲击容量 1 的作用域：恰好 1 个获批，其余全部
`IN_FLIGHT`；胜者提交后关联总数恰为 1，不会双双成功也不会双双失败。

### 3.5 判定开销不随历史/已确认总数增长

准入路径上：

- 过期回收只弹出堆顶 `deadline <= now` 的项，与已确认关联数、历史请求数无关；
- `confirmed` 与 `inFlight` 均为 O(1) 的 map 长度；
- 入堆 O(log inFlight)。

即开销只与**当前仍进行中的请求数**相关。证据：决策日志中每条事件都记录
判定时刻的 `confirmed / inFlight / baseline / reason`，并可由日志独立重放
（`replayJournal`），核验方不需要读取任何额外对外状态。

### 3.6 等价于某种串行顺序

所有判定在同一把锁内完成，事件按进入临界区的次序获得连续日志序列号；
该序列号即一个合法的等价串行顺序。随机测试
（`TestRandomizedEquivalence`，200 个种子 × 120 个操作）将同一序列送入
独立实现的朴素全局串行模型（全量扫描的 oracle），逐操作比对结果、
逐步比对 `(confirmed, inFlight, version)`，并在结束后比对每个请求的最终裁定。

### 3.7 被拒绝请求不留外部痕迹

拒绝路径不修改 `links / pending / version`。唯一新增内容是一条 REJECT
日志——日志是仅供核验的旁路证据，明确不属于业务状态（清空日志不影响任何
判定）。`TestRejectedLeavesNoTrace` 验证拒绝前后快照不可区分，且被拒的
requestID 可立即复用。

### 3.8 悬置请求（调用方中断）的唯一裁定依据

采用**心跳租约**：每个进行中请求持有 `deadline = now + TTL`，调用方在
最终确定前周期性 `Heartbeat`。裁定规则唯一且无歧义：

- `now < deadline`：不能确定中断，**绝不提前释放**；
- `now >= deadline` 且此后第一次有任意操作进入临界区（或显式 `Reap`）：
  原子回收，记 EXPIRE，名额立即释放。

因此名额既不会因悬置请求被永久占用（TTL 上界），也不会在尚不能确定
中断时被提前让出。时钟通过 `Clock` 接口注入，生产用墙钟、测试用
可控假时钟；回收是惰性的（不设后台 goroutine），因此释放时刻在模型内
仍然是确定的——它发生在「第一个越过 deadline 的临界区操作」处。

## 4. 关键取舍

- **单一全局锁，而非按作用域分片或数据库行锁/信号量。** 临界区只做纯内存
  操作且无 I/O，单锁给出最简单的可线性化论证与串行顺序；在链接写入这个
  量级（毫秒级业务事务的准入）足够。若未来单锁成为瓶颈，可按
  `ScopeKey` 哈希分片，分片内仍沿用同一算法，语义不变。
- **准入即预占（两阶段），而非「判定时直接写关联 + 失败补偿删除」。**
  后者无法表达「另一个请求还没成功，但名额已被预订」，正是幻影的来源。
- **惰性回收 + deadline 堆，而非后台清理 goroutine 与定时器。** 避免了
  时钟回调与请求操作竞争同一状态的第二线性化点；释放语义完全由
  「deadline + 下一次临界区进入」确定，可重放。
- **被拒不排队、不自动重试。** 需求明确拒绝自动唤醒；排队还会引入
  唤醒公平性与唤醒风暴问题。调用方重试是显式的新请求，带新版本号。
- **基线版本只随成功提交推进。** 回滚/过期/拒绝都不改变版本，因此这些
  路径在乐观并发协议中对读者「等于没有发生过」。

## 5. 被放弃的方案

1. **读时加共享锁、写时升级排他锁（两阶段封锁）。** 锁升级与调用方崩溃
   会造成死锁/永久占用，仍需租约兜底；且跨存储持锁时间不可控。
2. **数据库唯一约束 + `count(*)` 复查。** 唯一约束只能防重复关联，
   无法把「进行中未提交」计入基数；`count(*)` 在提交前看不到别人的
   未提交事务（或退化为全表谓词锁，代价随关联总数增长，违背开销要求）。
3. **分布式信号量 / etcd 租约。** 对单进程本体服务引入外部依赖，且
   「恰好一成一败」仍需 fencing token 与版本号配合，复杂度更高；本实现
   的接口形状（租约 + 版本）在需要时可平移到该实现。
4. **条件变量等待空位。** 与「被拒者不自动重试」冲突，且释放时惊群会让
   多个等待者同时竞争，需要额外一轮裁决，收益为零。
5. **后台定时器逐个请求到期。** 定时器数量随进行中请求线性增长，且
   定时器回调与显式回滚的竞态需要额外同步；deadline 堆一次回收全部
   到期项，更简单且可在日志中重放。

## 6. API 摘要

```go
g := ontology.NewGuard(ontology.Config{LeaseTTL: 30 * time.Second})
g.EnsureScope(ontology.ScopeKey{LinkType: "Employee.department",
    Field: "employees", ObjectID: "dept-1"}, 10)

d := g.Begin(ontology.BeginRequest{
    RequestID: "uuid-1", Scope: key,
    SourceID: "emp-7", TargetID: "dept-1",
    ObservedVersion: snapshot.Version, // 读取名额时看到的版本
})
switch d.Admitted {
case true:  // 业务工作期间周期性 g.Heartbeat("uuid-1")
           // 成功 g.Commit("uuid-1")；失败 g.Rollback("uuid-1")
default:    // d.Reason: BASELINE_CONFLICT / CONFIRMED_FULL / IN_FLIGHT / DUPLICATE
}
```

HTTP 演示见 `cmd/server`，GET `/journal` 导出完整决策日志。

## 7. 本地验证方法

```bash
# 全量测试（含 200 种子随机对照、64 路最后名额争夺、多轮压力）
go test ./...

# 竞态检测 + 多次重复（暴露偶发数据竞争）
go test -race -count=3 ./...

# 只看随机等价与重放
go test -run TestRandomizedEquivalence -v ./ontology

go vet ./...
gofmt -l .
```

若环境中 `go` 不在 PATH 且构建缓存目录只读，可：

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache GOPATH=/tmp/gopath
```

演示服务（环境允许监听端口时）：

```bash
go run ./cmd/server -addr :8080 -lease-ttl 30s
```
