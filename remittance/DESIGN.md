# 跨境汇款：报价锁汇 / 限额占用 / 合规审核 —— 设计说明

代码位于 `remittance/`，无第三方依赖，仅用 Go 标准库。

## 1. 模块划分

| 文件 | 职责 |
| --- | --- |
| `errors.go` | 唯一错误类型 `Error` + 11 个可区分的 `ErrorCode`，提交/审核各自的错误按优先级枚举。 |
| `types.go` | 入参、出参、状态、限额等纯数据结构；常量 `secondsPerDay=86400`、`annualDays=365`、`rateScale=1_000_000`。 |
| `engine.go` | `Engine`：单互斥锁串行化全部操作、全局单调时钟 `lastNow`、账户/报价/汇款注册表、逾期物化入口。 |
| `account.go` | 单汇款人的状态：限额、按日占用桶 `buckets`、滚动年度聚合 `annualUsed`、待审核 FIFO `pending`、幂等表；金额 128 位换算；纯函数占用投影 `projectUsage`；状态变更 `advance/reserve/release/materialize`。 |
| `quote.go` | 报价记录与有效性判定（`!consumed && now <= expiresAt`）。 |
| `transfer.go` | 汇款内部记录与对外快照。 |
| `quote_flow.go` | 报价申请（校验优先级：参数非法 > 时钟回退）。 |
| `submit.go` | 汇款提交：9 级错误优先级、制裁、幂等、报价、floor/ceil 换算、纯函数限额预演、一次性提交占用。 |
| `review.go` | 批准 / 拒绝 / 撤回：参数 > 时钟 > 存在 > 状态（含逾期）；拒绝路径不改状态。 |
| `query.go` | `GetTransfer` / `Usage`：被接受的查询会物化逾期并推进时钟。 |

测试：

| 文件 | 内容 |
| --- | --- |
| `remittance_test.go` / `edge_test.go` | 题目点名的全部边界。 |
| `naive_model_test.go` | 独立朴素模型：每次操作全量扫描历史重算占用、全量重算逾期。 |
| `differential_test.go` | 40 个固定种子 × 400 个随机操作双模型对照，日志打印输入/输出/判定依据。 |
| `concurrency_test.go` | `-race` 下的并发限额与并发幂等。 |
| `bench_test.go` | 历史规模 1k/10k/100k 下的提交与 1 万汇款人下的查询基准。 |

## 2. 关键规则到实现的映射

### 2.1 时间与时钟

- 全局只有一个 `lastNow`。所有携带 `now` 的操作（申请、提交、批准、拒绝、撤回、查询）先做时钟检查；`now < lastNow` 直接返回 `ErrCodeClockBackward`。
- **被拒绝的操作绝不写 `lastNow`**，也不写任何业务状态。所有错误都在“变更区”之前返回；提交在限额校验阶段使用纯函数投影（见 2.4），因此限额被拒也不会顺带物化别人的逾期单。
- 制裁名单维护与汇款人注册不带时间、不推进时钟：它们是外部管理事实。

### 2.2 报价

- `expiresAt = createdAt + T`；有效条件 `now <= expiresAt`：到期恰等有效，晚一秒过期。
- 报价只有一个布尔位 `consumed`；仅在提交通过**全部**校验、进入提交变更区时置位。制裁、过期、限额等任何拒绝都不消耗报价（`TestSanctionDoesNotConsume`、`TestRejectedSubmitLeavesNoTrace`）。

### 2.3 金额换算

- 乘积用 `math/bits.Mul64` 保 128 位，再 `Div64`：
  - 入账目标额 `floor(a*r/1e6)`（`mulDivFloor`）；
  - 额度占用 `ceil(a*r/1e6)`（`mulDivCeil`，加 `d-1` 在 128 位域完成）。
- 结果超过 `int64` 报参数非法。

### 2.4 限额：按日桶 + 滚动年度聚合 + 纯函数预演

- `buckets[day]` 是该日**仍生效**的占用合计；成功出款与待审核都计入，失败/撤回/逾期即从桶中减去。
- `annualUsed` 是窗口 `(anchorDay-365, anchorDay]` 内所有桶之和的增量维护；`advance(day)` 把锚点向前推，每滚出一天就从 `annualUsed` 扣除并删桶，每个桶只处理一次。
- 待审核队列 `pending` 按提交顺序排列：被接受操作的 `now` 单调不减，`deadline=submittedAt+R` 也单调不减，所以队首即最早逾期者。
- **关键取舍：提交的限额校验不能先物化**（否则限额拒绝会留下逾期失败的副作用）。`projectUsage(now, occ)` 是纯函数：
  1. 只读扫描 `pending` 队首，聚合“此刻已逾期但尚未物化”的待审核占用（按原占用日）；
  2. 从当前 `annualUsed` 减去“推进到 `day` 会滚出窗口的整天桶”和“仍在新窗口内的逾期释放”；
  3. 日占用 = `buckets[day] - 当日逾期释放 + occ`。

  校验用投影值，提交通过后才真正 `materialize + advance + reserve`，两者口径一致，故“预演通过 ⇔ 提交成功”。
- 三项判断全部是 `<=`，恰等限额满足；错误顺序单笔 → 日 → 年度。

### 2.5 审核、逾期与释放归属

- `target >= ReviewThreshold`：提交后状态 `Pending`，占用已计入但不出款，`deadline = now + R`。
- 批准：`now <= deadline`（含第 R 秒）才接受，状态转 `Succeeded`，占用保留。
- 明确拒绝、撤回：接受后状态转 `Failed`，`decidedAt=now`，从**原占用日**桶释放。
- 逾期：不依赖任何定时器。任何被接受的操作都会调用 `materializeSender(now)`，把 `deadline < now` 的待审核单置为 `Failed`，`decidedAt = deadline+1`（“逾期事实成立的第一秒”，保证纯函数式、可重放），并从原占用日释放。
- 审核/撤回的拒绝路径**不物化**：逾期与否用纯时间比较 `deadline < now` 报 `IllegalState`，因此对一个已逾期单调用批准再调用撤回，不会在第一次调用时提前改变状态。
- `release(releaseDay, occ)` 只作用于原日桶；该桶若已滚出年度窗口（跨年度后才失败），删键即可，`annualUsed` 不再二次扣减——释放永远不会记到“失败发生的当天”（`TestReleaseAcrossDay`、`TestReleaseAcrossYear`）。

### 2.6 制裁与幂等

- 提交顺序：参数 → 时钟 → 制裁 → 幂等。制裁是外部事实，幂等重放也要先过当前名单。
- 幂等表按汇款人隔离，只比较业务参数（报价编号、收款人、键；`now` 不参与——重放天然更晚）。
- 完全一致的重放在制裁检查之后、报价查找之前返回**首次提交时的原始结果**（含原始 `Pending` 状态），标记 `Replay=true`，不重复占用、不消耗报价。重放本身是被接受的操作：先物化、推进时钟。
- 被拒绝的提交不写幂等表（所以同键同参数在放开限额后仍可首次成功）。

### 2.7 并发

- 一把 `sync.Mutex`。所有操作在锁内完成读改写，天然线性化；“等价于某个串行顺序”由互斥保证，无需更细粒度锁或乐观并发。单实例业务校验全部是内存常数级操作，锁粒度不是瓶颈。

## 3. 复杂度（与历史汇款总数、汇款人总数无关）

设 365 天窗口内该汇款人的占用桶数为 B（B ≤ 365），当前待审核笔数为 P（活跃集合，不是历史总数），本次逾期笔数为 k。

- 提交：限额预演 O(B + k)，B ≤ 365；成功提交本身 O(1) 摊还。
- 查询占用：`advance` 每跨过一天 O(1)，每个桶一生中只被删一次；`Usage` 为 O(跨过天数) 摊还 O(1)，读两个整数。
- 批准/拒绝/撤回：O(P) 在当前待审核集合中摘除（按提交序的短切片），与历史总数无关。
- 逾期物化：每笔待审核汇款只被弹出一次，整体对每笔 O(1)。
- 全部路径只访问**本汇款人**的 `account`，不遍历 `accounts`，故与汇款人总数无关。

可验证方式：

- 基准 `BenchmarkSubmitAfterLongHistory/{1k,10k,100k}`：同一账户历史汇款扩大 100 倍，单步提交维持亚微秒级（实测约 0.4–0.9 µs，3 次分配），不随历史增长。
- `BenchmarkUsageManySenders`：存在 10 000 个汇款人时查询固定少数账户，耗时不随人数变化。
- 朴素模型本身是 O(历史汇款总数) 的全量扫描，长期随机序列下若生产结构退化为历史相关，双模型对照会显著变慢并仍保持结果一致；基准从耗时侧给出直接证据。

运行：

```bash
go test -run=NONE -bench=. -benchmem ./remittance/
```

## 4. 被放弃的方案

1. **全局逾期最小堆（container/heap）**：初版用全局堆按 deadline 排序。问题是限额校验需要“假设物化后”的视图，若为校验而弹出堆，限额被拒时必须回滚，且全局堆还会替*其他*汇款人物化状态，违背“拒绝不留痕”。改为**每汇款人按提交序的 FIFO**（deadline 天然单调），配合纯函数 `projectUsage`，提交预演零副作用；全局堆的跨账户排序能力并没有被任何规则需要。
2. **后台定时器/定时任务处理逾期**：引入墙钟与线程调度，相同操作序列无法保证逐字节复现。改为“惰性、确定性物化 + `decidedAt=deadline+1`”，任何 `now` 下的结果只由已接受操作与该 `now` 决定。
3. **提交前先物化再校验限额**：实现简单，但限额/报价等拒绝会顺带提交状态变更（违反“被拒绝操作不得改变任何状态”）。改用纯函数投影，换来提交路径的只读预演。
4. **为幂等重放返回“当前状态”**：需求要求“返回原结果”，故幂等表固化首次 `SubmitResult`（原始 `Pending`），当前状态请走 `GetTransfer`。
5. **细粒度分账户锁 / 无锁结构**：规则要求全局时钟单调（跨汇款人也不得回退），一把锁最直接地给出线性化与全局时钟顺序，且操作均为内存 O(1)，放弃分片锁的复杂度。
6. **浮点或 `float64` 计算金额**：存在精度风险，全部改为 `bits.Mul64/Div64` 的 128 位整数运算。

## 5. 本地验证方法

需要 Go 1.26+（`/usr/local/go/bin` 在 PATH 中即可）。

```bash
# 全部测试（含朴素模型随机对照，-v 可看每步输入/输出/判定依据）
go test ./...
go test -v -run TestDifferentialRandom ./remittance/

# 竞态 + 重复运行
go test -race -count=3 ./remittance/

# 基准（复杂度证据）
go test -run=NONE -bench=. -benchmem ./remittance/

go vet ./...
gofmt -l .
```

覆盖到的点名边界：报价到期恰等/晚 1 秒（`TestQuoteExpiryBoundary`）、floor 与 ceil 差异（`TestFloorVsCeil`）、占用恰等限额（`TestExactLimitSatisfied`）、审核第 R 秒恰等与晚 1 秒（`TestReviewDeadlineBoundary`）、跨日/跨年度释放归属（`TestReleaseAcrossDay`/`TestReleaseAcrossYear`）、制裁不消耗报价与幂等键（`TestSanctionDoesNotConsume`）、幂等重放与冲突（`TestIdempotencyReplayAndConflict`）、被拒操作不留痕（`TestRejectedSubmitLeavesNoTrace`、时钟回退用例）、并发不突破限额（`TestConcurrentSubmissionsNeverExceedLimits`、`TestConcurrentSameIdemKey`）。
