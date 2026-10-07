# 逻辑删除与可见性服务 — 设计说明

代码位于 `retention/`（包 `ontology/retention`）。

## 1. 状态机（互斥、单向）

```
ALIVE ──SoftDelete(graceDeadline)──▶ GRACE ──Undelete──▶ ALIVE
  │                                   │
  │                                   └── now≥graceDeadline 后，
  │                                       下一次涉及该对象的操作
  │                                       自动 ──▶ ARCHIVED（终态）
  └──Freeze(d)──▶ FROZEN ──now≥freezeDeadline 后显式 Archive──▶ ARCHIVED
```

- 四种状态互斥，`object.state` 是唯一真源。
- 任何逆向/越向请求返回 `ErrIllegalTransition`，拒绝时不改状态、截止时刻、时钟。
- 宽限到期的归档是**惰性**的：不依赖后台扫描线程，而是在“下一次任何涉及
  该对象的操作”持锁后无条件执行。查询也是涉及对象的操作，因此读路径同样
  先做提升；被拒绝的请求也会先触发提升（提升是系统动作，不消耗调用方请求）。
- 冻结到期**不做**惰性自动归档：规格要求 FROZEN“只能在冻结期满后转入
  已归档”，即仍需一次 `Archive`（或运维性的 `Advance` 批量扫描），这一选择
  避免冻结对象无声消失，语义上更保守。

## 2. 时间模型与边界语义

- 时间是不透明的单调整数（默认系统墙钟毫秒；测试注入 `LogicalClock`）。
- 宽限：`graceDeadline` 必须 `> now`；到期条件为闭区间 `now >= deadline`，
  所以“恰好到达”（`now == deadline`）与“刚过去”（`now > deadline`）都归档，
  `now == deadline-1` 仍可撤销。
- 冻结：`duration > 0`，`freezeDeadline = now + duration` 在进入时一次写定，
  之后任何操作都不重算/延长；放行条件同样是 `now >= freezeDeadline`。
- 被拒绝的转换绝不推进时钟；时钟只能由 `Advance`/墙钟前进改变。

## 3. 可见性

| 身份 | ALIVE | GRACE | FROZEN | ARCHIVED |
|---|---|---|---|---|
| 一般使用者 | 可见 + 属性 | 视为不存在 | 视为不存在 | 视为不存在 |
| 数据管理员 | 可见 + 属性 | 可见 + 状态/截止 | **只见状态 + freezeDeadline** | 可见 + 状态 |

- 一般使用者对非存活对象返回“存在但不可见且不泄露状态”（API 层可统一映射
  成 404，避免泄露存在性）。
- 出边链接没有删除标记，可见性在唯一判定点 `sourceVisibleLocked` 跟随源对象：
  源不可见 ⇒ 全部出边不可见。目标对象的状态不影响出边可见性。
- **关键取舍**：管理员对 FROZEN 对象能看到状态与截止时刻，但业务属性被遮蔽；
  出边属于业务关系数据，按同一保密理由在 FROZEN 管理员视图下一并遮蔽。
  若产品上希望管理员仍能遍历冻结对象的边，只需调整 `sourceVisibleLocked`
  一处（有测试矩阵锁定行为）。

## 4. 错误模型（四类互斥、固定次序）

判定严格按以下次序短路：

1. `OBJECT_NOT_FOUND`：对象不存在（对 AddEdge，源、目标依次判定）。
2. `ILLEGAL_TRANSITION`：当前状态不允许该方向。
3. `INVALID_PARAMETER`：宽限截止时刻不晚于当前时刻，或冻结时长 ≤ 0。
4. `FROZEN_NOT_EXPIRED`：在冻结未满时撤销/归档。

次序的一个刻意处理：`Undelete`/`Archive` 遇到 FROZEN 未满时，第 4 类优先
于第 2 类——因为 `FROZEN→ARCHIVED` 方向合法，只是被“未满”守卫拦下；
而对 FROZEN 调 `SoftDelete`/`Freeze` 仍报第 2 类。该次序在
`TestErrorPrecedence` 与朴素模型中被双重锁定。

## 5. 并发控制

- 一把 `sync.RWMutex`：所有变更（含惰性归档）在写临界区内完成；
  查询在同一临界区内完成到期提升 + 可见性判定 + 出边枚举。
- 这给出可线性化（linearizable）语义：并发结果等价于某一全局顺序，
  查询不会读到两态之间的中间形态；`QueryWithEdges` 保证对象与出边
  在同一次查询内结论一致。
- `LogicalClock` 自带互斥，时钟可在锁外被安全读取（`-race` 验证）。

**被放弃的方案**：
- 每对象分片锁 + 延迟删除队列：吞吐更高，但跨对象的出边一致性判定需要
  额外协调，且惰性归档的“下一次操作”语义需要额外同步原语；当前规模下
  单锁更简单、正确性可证。分片是未来纯吞吐优化，不改变 API 语义。
- 事件溯源全量历史做线上判定：语义直观，但可见性成本随历史线性增长，
  直接违反 O(1) 要求。全量历史仅保留在**测试参照模型**里。

## 6. O(1) 历史复杂度与可验证证明

- 生产对象只保存 `state + graceDeadline/freezeDeadline`，转换是就地覆盖，
  不保留历史记录；一次可见性判定只读这 1 条当前态记录。
- 证明方式（可复现）：`object.probes` 探针在每次判定读取当前态时 +1。
  `TestVisibilityIsO1InHistoryLength` 分别制造 400 与 1200 次历史转换，
  断言 3 次判定恰好产生 3 次探针增量、1 次判定恒为 1 次——增量不随历史
  长度变化，即 O(1)。运行：
  `go test ./retention/ -run TestVisibilityIsO1InHistoryLength -v`
- 另由朴素模型（见下）独立保留全部历史做结果对照，从侧面保证“不看历史”
  没有牺牲正确性。

## 7. 审计日志

每次操作（含被拒绝者与自动归档）写一条 `AuditEntry`：全局序号 `seq`、
判定时刻 `at`、操作名、对象、输入参数、判定前/后状态、成功与否、
错误码与输出摘要。条目可同时落盘（JSON Lines）并保留在内存中供测试核对。

## 8. 测试清单与本地验证

- `TestGraceExactBoundaryAndJustPast`：截止恰好到达 / 刚过去 / 前一刻撤销。
- `TestFreezeBoundaries`：期满前一刻拒绝撤销与归档、恰好期满放行、
  归档终态、deadline 不被重算。
- `TestVisibilityMatrixWithEdges`：两身份 × 四状态 × 对象/出边组合，
  含 GRACE 撤销后出边恢复。
- `TestErrorPrecedence`：四类错误次序与“拒绝不改状态/截止/时钟”。
- `TestConcurrentTransitionsAndQueries`：8 查询 + 4 写 + 时钟推进交织，
  断言快照内不变量；用 `-race` 运行。
- `TestDifferentialAgainstNaiveOracle`：40 个随机种子 × 300 步随机操作，
  与独立朴素实现（`naive_test.go`，保留完整历史、每步 O(历史) 重放）
  逐条比对错误码、状态、两身份可见性、截止时刻与出边可见性。
- `TestAuditContents`：日志字段与 JSON Lines 落盘条数。

```bash
export GOCACHE=/tmp/gocache          # 如默认缓存目录只读
go test ./...
go test -race -v ./retention/
go vet ./... && gofmt -l .
```
