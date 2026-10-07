# 跨链接聚合视图子系统 — 设计说明

## 1. 问题与目标

视图把通过某条链接关系相连的一组对象，按各自的时间类属性分组（UTC 日桶）并排序。
对象分属不同对象类型，每个对象类型的“默认时区定义”是**版本化**的（`TzDefVersion`）。
核心约束：

- 分组/排序在统一基准时区（UTC）下进行；
- 每个对象按其**写入时刻**生效的默认时区版本解释，而非维护时刻的最新版本；
- 已归入分组的对象不因随后的时区版本迁移而移动（不倒退、不重复计入），
  除非该对象的时间属性被重新写入；
- 滞后到达的变更事件，其分组归属与到达顺序无关；
- 增量维护、迁移、查询可并发，结果等价于某个全局串行顺序；
- 查询开销不随历史事件总量 / 历史迁移次数线性增长。

## 2. 核心设计：写入时刻锚定（write-time anchoring）

平台为每次时间属性写入分配全局逻辑序号 `WriteSeq`，并在**写入时刻**把当时生效的
默认时区版本（`AnchoredTzVersion`）及其时区 ID（`AnchoredZoneID`）固化进事件
（`Platform.WriteTimeProperty`，ontology/platform.go）。

事件因此是**自包含**的：视图解释事件时不需要查注册表，也就不依赖
“迁移记录是否已到达视图侧”。这是“最终分组归属与到达顺序无关”的关键——
它不仅覆盖事件滞后，也覆盖**迁移记录本身滞后**的情形（差分测试曾抓到该缺陷：
事件锚定了迁移后版本、但迁移记录尚未到达，注册表查不到该版本）。

视图侧的三条规则共同保证顺序无关性：

1. **last-write-wins**：同一对象只应用 `WriteSeq` 最大的事件，其余为过期事件直接忽略；
2. **锚定不可变**：分组键 = `f(事件内容)`，与维护时刻的注册表状态无关；
3. **单一归属**：`members` 索引保证一个对象至多属于一个分组，重写入先移除旧归属。

“不倒退”由更强的性质推出：迁移操作**从不触碰**视图中的既有归属
（`MigrateDefaultTz` 只追加版本定义），因此迁移前后视图快照必然不变
（`TestMigrationDoesNotMoveExistingMembers`）。

## 3. 排序与次序裁决

- 分组键：归一化 UTC 时刻的日桶（`floor(unix/86400)`，对负值做数学下取整）。
- 组内顺序：`(归一化时刻, 对象类型 ID, 对象 ID)` 字典序（`memberLess`，ontology/view.go）。
  裁决规则只依赖元素自身字段，与增量处理先后无关
  （`TestTieBreakDeterministic` 用两种相反写入顺序验证结果一致）。

## 4. 错误分类与报告优先级

| 优先级 | 类别 | 触发条件 |
|---|---|---|
| 1（最高） | `ErrLinkEndpointTypeMissing` | 链接关系未注册，或链接两端 / 事件对象类型不存在或已删除 |
| 2 | `ErrGroupingPropertyDeprecated` | 对象类型当前版本已废弃分组所用时间属性 |
| 3 | `ErrNoDefaultTimezoneAtWrite` | 事件锚定版本为 0（写入时刻未定义默认时区）或锚定时区未知 |
| 4（最低） | `ErrMigrationValidationFailed` | 迁移校验失败（版本号/生效序号不递增、时区未注册、类型缺失） |

一次 `Maintain` 可能同时触发多类错误：`MaintenanceResult.Errors` 记录各类触发次数，
`TopError` 只报告优先级最高的一类（`TestErrorPrioritySingleReport` 逐级验证）。

错误下的安全性：出错事件被整体拒绝——若它是该对象的最新写入，对象被从视图中
驱逐（而不是保留旧值或部分应用），因此错误不会产生重复计入或时间倒退
（`TestErrorsCauseNoObservableAnomaly` + 每次查询后的不变量校验）。

迁移校验规则：版本号严格递增、生效序号严格递增（禁止版本间互相覆盖）、
时区已注册、类型存在且未删除。生效序号**允许**小于当前逻辑时钟
（迁移记录可能滞后到达），因为事件锚定不可变，滞后迁移不会改变任何已锚定解释。

## 5. 并发模型

所有公开操作（写入、注入、迁移、维护、查询、朴素重放）都在 `Platform` 的
单把 `sync.RWMutex` 下执行：写操作互斥，查询用读锁。因此任意并发调用的可观察
结果等价于按锁获取顺序的串行执行（可线性化）。

`TestConcurrentOpsLinearizable`（`-race` 下运行）：并发写入/迁移/查询期间每次
查询都校验不变量；收敛后增量视图与朴素模型对全量历史的串行重放逐条一致。

## 6. 查询复杂度与可独立验证性

视图维护增量索引：`groups`（分组键 → 有序成员）、`keys`（有序分组键）、
`members`（对象 → 归属）。查询只遍历当前索引：
**O(分组数 + 结果条目数)**，与历史事件总量、历史迁移次数均无关。

独立验证方式（不依赖对实现的信任）：

1. **插桩计数**：`Query` 返回 `QueryStats{GroupsVisited, EntriesVisited}`，
   记录本次查询实际访问的条目数。`TestQueryCostIndependentOfHistory` 证明
   历史从 500 增长到 50000 条时访问数恒定；`TestQueryCostIndependentOfMigrations`
   证明 1000 次迁移后访问数不变。
2. **基准测试**：`go test -bench=QueryHistoryScaling -benchtime=2000x ./ontology`
   显示历史 1e3 → 1e5 时单次查询耗时近似恒定（本机约 20µs → 19µs）。

注意：`Platform.history` 全量事件历史**只**供朴素模型对照与审计，
视图本身从不读取——这正是复杂度证明成立的结构保证。

## 7. 测试体系

| 测试 | 覆盖点 |
|---|---|
| `TestMigrationLateEventInterleavings` | 4 事件全排列 × 迁移插入位置 × 维护粒度 = 480 种交织，结果全等 |
| `TestWriteSeqBoundaryAnchoring` | 写入序号在生效序号边界（S-1/S/S+1）的锚定归属 |
| `TestLateEventAfterMigrationUsesAnchoredVersion` | 迁移后到达的滞后事件按迁移前版本分组 |
| `TestTieBreakDeterministic` | 同组同值裁决规则及顺序无关性 |
| `TestDifferentialRandomOpSequences` | 50 种子 × 300 随机操作，增量视图 vs 朴素全量重建逐条对照 |
| `TestConcurrentOpsLinearizable` | 并发可线性化（`-race`） |
| `TestQueryCostIndependentOf*` | 查询开销与历史/迁移次数无关 |
| `TestErrorPrioritySingleReport` 等 | 四类错误各自触发与优先级 |

**审计**：每次判定（assign/stale/skip-error/evict/migrate）都记录
`DecisionRecord`（输入、锚定时区版本、归一化结果、分组结论），
由 `Platform.DecisionLog` 导出。差分测试把每次对照的输入、时区版本与结论
写入 JSONL 审计日志（默认 `/tmp/ontology-audit/`，可用 `ONTOLOGY_AUDIT_DIR` 覆盖）。

## 8. 被放弃的方案

1. **维护时刻按最新版本解释**：直接违反“按写入时刻版本”的要求，且迁移会导致
   既有对象分组倒退。放弃。
2. **迁移时全量重算视图**：能保证一致性，但迁移成本随视图规模线性增长，
   且无法满足“已归入对象不因迁移而移动”。放弃。
3. **事件只锚定版本号、视图侧查注册表解析时区**：被差分测试证伪——迁移记录
   本身可能滞后到达，导致“版本存在与否”取决于到达顺序。改为事件自包含
   （锚定版本号 + 时区 ID）。注册表只负责版本演进校验，不参与事件解释。
4. **事件溯源 + 查询时重放**：查询开销随历史线性增长，违反复杂度约束。
   历史仅保留用于测试对照，不服务查询。
5. **细粒度锁（每视图/每类型一把锁）**：可线性化论证复杂、收益不明；
   当前规模下单互斥锁足够，且等价串行化的证明是平凡的。如需扩展，可按
   视图分片加锁，但须重新论证跨视图操作（类型删除）的串行等价性。
6. **真实 IANA 时区库**：引入对宿主 tzdata 的依赖，测试不可复现。
   采用固定偏移时区注册表；生产接入时只需把 `Zone` 替换为 IANA 实现，
   锚定与分组逻辑不变。

## 9. 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath   # 默认缓存目录只读时

go test ./...                # 全量测试
go test -race ./...          # 竞态检测
go test -run Interleav -v ./ontology   # 交织穷举（480 例）
go test -run Differential -v ./ontology  # 差分对照 + 审计日志路径
go test -bench=QueryHistoryScaling -benchtime=2000x ./ontology  # 查询复杂度基准
go run ./cmd/server          # 端到端演示
go vet ./... && gofmt -l .   # 静态检查与格式
```
