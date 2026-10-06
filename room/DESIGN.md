# 多人对局房间生命周期服务 — 设计说明

## 1. 职责划分

代码按“状态机 / 在室登记 / 结算账本 / 朴素参考模型”拆分，互不越界：

| 文件 | 职责 |
| --- | --- |
| `room.go` | 生命周期状态机：阶段迁移、惰性到期、拒绝次序、操作语义、快照 |
| `registry.go` | 在室玩家集合：加入次序链表、房主（链表头）、未就绪计数器 |
| `ledger.go` | 结算账本：当前上报与每胜者票数，支持 O(1) 改报 |
| `types.go` / `errors.go` | 纯数据：阶段、作废原因、配置、快照、拒绝原因 |
| `naive.go` | 独立朴素模型（切片 + 全量线性扫描），仅测试使用 |
| `*_test.go` | 场景用例、1600 组随机差分、并发等价性、常数时间基准 |

## 2. 状态机与惰性计时

六个阶段与题面一一对应。两个计时器都采用“惰性求值”：

- **就绪倒计时**：进入 `countdown` 时只记录起算时刻 `cStart`，到期时刻
  为 `cStart + C`。房间里不存在任何后台 goroutine/定时器。
- **上报期限**：进入 `settling` 时记录 `reportDue = End.now + R`。

每个对外方法的统一入口顺序（`Join/SetReady/Leave/End/Report/Snapshot`
全部相同）：

1. 参数校验（空标识、空胜者、`now` 越界、`New` 的配置区间）；
2. 取锁，时钟回退检查（`now < r.now` 直接拒绝，无任何副作用）；
3. **时钟推进** `r.now = now`，随后 `expireLocked()` 先做惰性到期裁决；
4. 按 终态 → 阶段 → 在室/名单 → 房主权限 → 状态冲突 的次序检查并执行。

关键后果（测试均有覆盖）：

- 恰等于到期时刻视为到期：`now >= cStart+C`、`now >= reportDue`。
- 倒计时到期**以到期时刻开局**（`StartAt = cStart+C`），而非本次操作的
  `now`；因此到期时刻之后发起的“取消就绪/加入”不再能阻止开局，而是先
  开局再按 `playing` 阶段规则被拒（到期先于操作生效）。
- 上报到期后任何 `Report` 被拒：到期在第 3 步已把房间推进到终态，
  第 4 步只能得到 `terminated`。
- 倒计时期间“离开一名已就绪玩家后条件仍成立”时，不重算起算时刻：
  `Leave` 分支检测到 `size>=L && allReady` 时调用
  `startCountdownIfReadyLocked`，但该函数只在 `waiting` 态写 `cStart`，
  故 `countdown` 态下 `cStart` 原样保留。
- 等待态任何操作完成后条件成立即进入倒计时（含“最后一名未就绪者
  离开”）：所有写操作尾部统一调用 `startCountdownIfReadyLocked`。

## 3. 关键裁决规则

- **房主迁移**：房主永远是“加入次序链表”的头节点。`Leave` 直接从链表
  摘除节点，头节点自动变为最早加入者；进行中原房主离开先写入 `quit`
  再摘除，因此既是“中途退出”（留在对局名单、上报资格取消）又完成迁移。
- **进行中不足两人**：以 `alive`（名单中仍在室人数）判定，离开后
  `alive < 2` 立即 `voided/too_few`，与是否已有上报无关。
- **结算一致**：`alive` 名仍在室对局玩家全部上报后，账本中胜者只有
  一种则 `ended`，否则 `voided/dispute`。
- **超时过半**：到期时若已上报者全部一致，且
  `2*reported > alive`（严格过半），以该胜者 `ended`；其余一律
  `voided/timeout`。无人上报时没有“一致胜者”，同样超时作废。
- **胜者合法性**：空胜者是 `invalid_arg`（参数级）；非空但不在对局
  名单是 `not_present`（名单级），符合“玩家不在室或不在对局名单”的
  归类。
- **空房作废**：等待/倒计时中离开后无人在室，立即 `voided/empty`。
- **终态**：`ended/voided` 后变更操作统一返回 `terminated`（拒绝次序中
  紧随时钟回退，优先于阶段/身份）；`Snapshot` 查询不受限。

## 4. 性能论证（可验证）

要求：Join/SetReady/Leave/Report 不随历史事件数增长；全体就绪判断不
随在室人数线性增长。

- `registry.nodes`/`ready` 为哈希表；加入用尾插 O(1)，离开摘除链表
  节点并删除映射 O(1)，**已离开者的节点立刻释放**，故结构规模只等于
  当前在室人数，与累计加入/离开次数无关。
- 全体就绪用 `notReady` 计数器：Join 自增、SetReady 增减、Leave
  按其就绪状态修正，`allReady()` 仅判 `notReady==0`，O(1)。
- 房主即链表头，迁移 O(1)，不需要排序或扫描。
- `ledger` 只保存“当前一票”：`votes[winner]` 与 `given[user]`。
  改报撤销旧票、计入新票，各为常数次哈希操作，不保留历史。
- 名单成员判定用 `rosterSet`（O(1)）；`Snapshot` 是唯一 O(n) 的路径，
  它是显式查询、需要物化全部玩家，不属于被限制的四个变更操作。

实测（`go test -bench`，2,000,000 次）：同样 20 人在室，经历 0 次与
20,000 次 join/leave 历史后，`SetReady` 分别约 97ns 与 86ns；
`Report` 反复改报约 80ns。常数时间与历史无关的结论可由
`BenchmarkSetReadyNoHistory` 与 `BenchmarkSetReadyLargeHistory`
直接对比复现。

## 5. 被放弃的方案

- **真实定时器 / goroutine**：能主动推送开局，但与“惰性、以操作 now
  为准、结果仅依赖输入序列”冲突，且带来并发生命周期管理与时钟不可控
  问题；改为纯惰性求值后，状态是输入序列的确定函数，易测易复现。
- **每次全量扫描在室玩家判断就绪/房主**：实现直白（朴素模型即如此），
  但 Join/SetReady/Leave 会随人数与历史增长；产品代码因此采用链表 +
  计数器，朴素模型保留扫描版本作为差分对照。
- **append-only 事件日志 + 折叠计数**：利于审计，但计数与历史规模绑定，
  无法满足“开销不随历史事件数增长”；最终只保留当前态，审计需求可由
  调用方基于操作返回值另行持久化。
- **房主 ID 字段 + 离开时重选**：重选需要扫描或堆，且容易与“最早加入”
  语义在重加入场景出错；链表头天然表达该不变量，无需特例。

## 6. 本地验证

```bash
# 全量测试（含 -race 并发等价性）
go test -race -count=1 ./...

# 规定场景的详细输入/输出/判定依据日志
go test ./room/ -run TestScenario -v

# 1600 组随机序列与朴素模型差分；分歧时完整 trace 写入 /tmp/room_diff_trace.log
go test ./room/ -run TestRandomDifferential -v

# -v 下额外打印 3 组固定种子的完整对照日志
go test ./room/ -run TestLoggedDifferentialSample -v

# 常数时间验证：无历史 vs 2 万次 churn 历史
go test ./room/ -run=NONE -bench=BenchmarkSetReady -benchtime=2000000x

go vet ./... && gofmt -l .
```

差分测试每条操作都比对：拒绝原因（`ok/invalid_arg/clock_rewind/
phase/terminated/not_present/not_owner/conflict`）与全字段快照
（阶段、房主、在室次序、就绪、起算/到期时刻、名单、退出者、上报、
胜者、作废原因）。随机生成器刻意混入时钟回退、非法参数、局外人、
恰等于/差一秒到期、中途退出后上报等高风险情形。
