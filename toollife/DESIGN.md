# 刀具库寿命管理服务 —— 设计说明

本服务为数控加工场景提供刀具寿命的**预占（reserve）→ 记账（settle）→ 破损/换新/锁定**全生命周期管理，
支持多个加工通道并发申请，并给出确定、可复现的选刀结果。

## 1. 模块划分

代码位于 `toollife/`，按职责拆分为相互协作的小模块：

| 文件 | 职责 |
| --- | --- |
| `types.go` | 纯数据定义：寿命口径、选刀模式、刀具状态、配置与对外快照类型，不含逻辑。 |
| `errors.go` | 可区分的错误码 `Code` 与错误次序约定。 |
| `magazine.go` | **存储模块**：刀组 `Group`、刀具 `Tool` 的注册与查找；刀组内固定顺序 `order` 与按号索引 `byID`。 |
| `selection.go` | **选刀策略模块**：顺序首次适配、严格/宽松可承载判定、`无刀可用` 与 `暂无余量` 的区分。纯函数式判定，不改状态。 |
| `warning.go` | **寿命算术模块**：剩余寿命截断（不为负）、已用千分比（向下取整）。 |
| `service.go` | **并发与台账模块**：全局互斥、全局申请编号台账（map）、加锁顺序。 |
| `operations.go` | **操作模块（门面）**：`Apply/Settle/Abort/ReportBroken/Replace/Lock/Unlock/Query/Warnings`，编排上面各模块。 |

独立参照实现 `toollife/naive/`：不引用生产包，以单线程、无并发、直白的方式重写同一规约，
仅供差分测试对照，不进入生产路径。

## 2. 关键数据结构与不变量

- `Tool{ status, used, reserved, warned }`：`used` 为已记账寿命，`reserved` 为未结算预占之和，
  `warned` 记录本刀（自上次换新以来）是否已发过预警。
- `Group{ cfg, order []*Tool, byID map[string]*Tool }`：`order` 是选刀的唯一顺序来源；
  `byID` 让按号定位为 O(1)；换新原地替换 `order` 中同一槽位，保证"占据原顺序位置"。
- `requestRecord{ groupID, estimated, tool, state }`：申请台账。`state ∈ {open, settled, aborted}`。

核心不变量（每次状态变更后均成立）：

1. `reserved == Σ(该刀上 state=open 的申请的 estimated)`；
2. 严格模式下，任一时刻所有 `open` 预占 + `used ≤ lifeLimit`；
3. 已 `exhausted/broken/locked` 的刀不会被选刀选中；
4. 每把刀自换新后最多发出一次预警。

## 3. 操作语义要点

### 申请 Apply（预占）

顺序首次适配：选第一把 `status=available` 且可承载的刀。
- 严格：`used + reserved + estimated ≤ lifeLimit`（用 `estimated > limit-committed` 判定，杜绝加法溢出）；
- 宽松：仅要求 `used + reserved < lifeLimit`，允许这一次使用在记账时超出上限。

选中后 `reserved += estimated` 并登记 `open` 台账。**选刀失败先于台账登记，因此拒绝不改任何状态。**

幂等与冲突：
- 同一申请编号 + 相同（刀组，预计消耗）重复提交：直接返回原 `(刀号, 预占量)`，不再预占；
- 同一编号但内容不同：`ErrConflict`；
- 错误次序为 参数非法 > 刀组不存在 > 申请冲突：先确认刀组存在再判冲突
  （因此"同编号打到一个不存在的刀组"报刀组不存在而非冲突）。

### 记账 Settle

- `reserved -= estimated; used += actual`，即预占整体释放、实际消耗计入已用（实际可 <、=、> 预计）；
- 重复记账幂等：`settled` 状态直接返回，不二次累计；对 `aborted` 申请记账报 `ErrState`；
- 仅当刀当前为 `available` 且 `used ≥ limit` 才转 `exhausted`；
  **破损/锁定刀照常记账，但状态保持 broken/locked**（满足"申请与记账之间被破损/锁定，记账仍生效"）；
- 预警只在记账时评估：`before < warn ≤ after` 首次成立时发一次，预占从不触发预警。

### 中止 Abort

`reserved -= estimated`、不计消耗、台账置 `aborted`；重复中止幂等；对已结算申请中止报 `ErrState`。

### 破损 / 换新

- 破损：刀置 `broken`，**其上 `open` 预占保留**（后续记账仍计入已用），之后不再被选中；
  对已耗尽刀报破损是状态错误；重复破损幂等。
- 换新：仅 `broken` 刀可换新，且要求 `reserved == 0`（无未结算预占）否则拒绝；
  新刀 `used=0, status=available, warned=false`，原地占据顺序槽位，并重置预警资格。

### 锁定 / 解锁

- 锁定：`available → locked`，已有预占不受影响，之后不被选中；重复锁定幂等；
- 解锁：`locked → available`；**已耗尽（used ≥ limit）或破损的刀不能解锁为可用**。

### 无刀可用 vs 暂无余量

`chooseTool` 两遍扫描：
1. 找第一把立即可承载的刀，找到即返回；
2. 找不到时，只有当**所有刀都"仅因现存预占"而无法承载**（即可用、有预占、把预占清零后即可承载）
   才报 `ErrNoCapacity`（暂无余量，预占释放后可成功）；
   只要有一把刀是破损/锁定/耗尽、或自身余量确实不足（即便无预占也不行），即报 `ErrNoTool`。

## 4. 并发模型与可串行化

- 用**单把 `Service.mu` 串行化所有状态变更**。每个操作是一个临界区：校验（失败则在写入前返回）→ 状态变更。
  因此任何并发执行的可观察结果都严格等价于这些临界区的某个全序（互斥）串行执行，天然满足可串行化。
- 存储层另有 `Magazine.mu`，采用固定加锁顺序 `service.mu → magazine.mu`，避免死锁。
- "多个通道同时申请同一刀组不得把同一份剩余寿命预占两次"由
  `读 committed → 判定 → reserved += est` 同在一个临界区保证（读-判-写原子）。

**为什么选单把全局锁而非每刀组一把锁**：见第 7 节"被放弃的方案"。

## 5. 选刀复杂度：只与组内刀具数相关

- 选刀只遍历 `group.order`（O(k)，k=组内刀具数），不读取任何历史申请。
- 申请台账用 `map[reqID]*requestRecord`：幂等/冲突/记账命中都是 O(1) 平均，**不随历史申请总数增长**。
- 可验证证据见 `TestSelectionCostIndependentOfHistory`（两档历史 1k vs 100k，同探针数，
  时间比被阈值 3.0 约束）与 `BenchmarkApplyHistory1k / 100k`；
  正向对照 `TestSelectionCostGrowsWithToolCount` 证明扫描成本确实随刀具数增长
  （16 → 4096 把刀，扫描全组的申请耗时显著上升）。

## 6. 错误次序

`ErrInvalid < ErrGroupNotFound < ErrToolNotFound < ErrConflict < ErrState < ErrRequestNotFound < ErrNoTool/ErrNoCapacity`
（`<` 表示判定优先级更高）。每个操作都先做参数校验，再定位刀组/刀具，再做冲突与状态判定，
最后才可能选刀失败；任何被拒操作都在任何状态写入之前返回。

## 7. 关键取舍与被放弃的方案

- **单把全局锁 vs 每刀组锁/无锁 CAS**：
  *放弃*了按刀组分锁与 CAS 方案。它们能提高吞吐，但跨刀组全局唯一的申请编号（幂等/冲突）
  仍需一处全局权威，且会显著增加可串行化推理与测试成本。本题的关键正确性是
  "确定结果 + 不重复预占 + 可串行化"，单锁使这些性质可直接证明；
  选刀本身 O(k) 且临界区极短，单锁足够。若未来吞吐成为瓶颈，可在不改变对外语义的前提下
  把锁下沉到刀组、申请编号用分片 map，并继续用同一套差分测试回归。
- **顺序首次适配 vs 最佳适配**：
  规约要求"按顺序第一把"，故确定地采用首次适配；不做碎片整理等优化（会改变选刀结果）。
- **预警资格用每刀一个 `warned bool` vs 记录上次千分比**：
  规约是"首次跨线只发一次，换新后重新具备资格"，布尔标志正好且在换新时随新刀重置，
  无需保存历史曲线。
- **破损预占保留 vs 自动作废**：
  明确保留待结算预占（破损发生在加工途中，实际消耗仍应入账），所以换新必须等待预占清零。
- **记账不改写破损/锁定状态**：严格遵守"记账仍生效，但状态保持破损/锁定"，
  只有可用刀在达到上限时才转耗尽。

## 8. 本地验证方法

```bash
# 需 Go 1.26+；若 go 不在 PATH：export PATH=$PATH:/usr/local/go/bin
# 若 GOCACHE 默认目录只读：export GOCACHE=/tmp/gocache

gofmt -l .            # 期望无输出
go vet ./...
go test ./... -race -count=1

# 详细查看随机序列的输入/输出/判定依据
go test ./toollife/ -run TestDifferentialLoggedCase -v

# 历史长度两档对照（复杂度证据）
go test ./toollife/ -run TestSelectionCost -v
go test ./toollife/ -run XXX -bench BenchmarkApplyHistory -benchtime 1s

# 覆盖率
go test ./toollife/ -coverprofile=cov.out && go tool cover -func=cov.out | tail -1
```

验证矩阵：

- 边界功能：预占+已用恰等于上限、严格/宽松差异、实际>预计、破损刀未结算预占、
  换新拒绝条件、预警恰好跨线且只发一次、暂无余量 vs 耗尽、重复申请/重复记账（`service_test.go`、`extra_test.go`）。
- 差分：300 组随机多操作序列与朴素模型逐步对照 + 最终状态/预警对照（`TestDifferentialSerialReplay`）。
- 并发：60 组随机纯申请计划，4/8 通道在强制确定交织下与朴素模型一致，`-race` 无竞争（`TestDifferentialConcurrentEquivalence`）。
- 复杂度：历史 1k vs 100k 两档 + 刀具数 16 vs 4096 正反对照（`cost_test.go`）。
