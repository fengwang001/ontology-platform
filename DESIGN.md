# 团队看板卡片流转服务 — 设计说明

## 1. 范围与模块划分

| 模块 | 文件 | 职责 |
| --- | --- | --- |
| 错误模型 | `kanban/errors.go` | 9 类稳定错误码、判定原因（reason） |
| 快照类型 | `kanban/types.go` | `Card`、`Config` 等对外不可变值对象 |
| 看板内核 | `kanban/board.go` | 建板/建卡校验、状态容器、增量占用计数、快照 |
| 流转 | `kanban/move.go` | `Move` / `Reopen` / `ChangeOwner`、拒绝次序、容量与加急判定 |
| 依赖 | `kanban/dependency.go` | `AddDep` / `RemoveDep`、重复/未完成/成环判定、后继邻接表 |
| 服务层 | `kanban/service.go` | 单把互斥锁线性化所有操作、操作日志（输入/输出/依据） |
| 只读查询 | `kanban/query.go` | `GetCard`、占用数、加急卡、`CheckInvariants` |
| 朴素参考模型 | `naive/naive.go` | 独立重写的第二实现，每次容量判定全量扫描 |
| 测试 | `kanban/*_test.go` | 规则用例、拒绝次序、1500 组差分、并发竞态、基准 |

内核（`Board`）是无锁纯逻辑，单线程假设；`Service` 用一把 `sync.Mutex`
把每个公开操作整体串行化。并发拖拽因此等价于某个确定的串行顺序，
且“校验 + 提交”在同一临界区，天然消除 TOCTOU。

## 2. 状态表示

- `cards map[id]*card`：卡片含 `owner/col/version/expedited/prereqs(set)`。
- `colCount []int`、`ownerCount map[string]int`：增量占用计数。
- `successors map[pre]set(card)`：反向邻接，仅 AddDep/RemoveDep/Reopen 使用。
- `expeditedCard string`：进行中区域唯一加急卡指针，空串表示无。
- `lastNow int64`：上一次**被接受**操作的时钟。

## 3. 关键取舍

### 3.1 拒绝次序用“短路链”硬编码

次序固定为：
参数非法 → 时钟回退 → 卡片不存在 → 版本冲突 → 流转不合法 →
依赖（未完成/被依赖/成环/重复）→ 加急已占用 → 列上限已满 → 负责人上限已满。

实现上 `Move` 严格按该顺序逐条 return，先于任何状态写入；被拒路径不触碰
版本、计数与 `lastNow`。唯一需要“先写后判”的容量环节采用先释放、
判定失败即 `rollbackPlacement` 回滚离开列计数，保证拒绝不留痕。

### 3.2 容量“先释放再判定”，而不是用增量补丁

进行中列之间前移时，先执行离开列的 `colCount-- / ownerCount--`，
再对目标列做 `+1` 判定。这样同一负责人从 col1 前移 col2 时计数经历
`1→0→1`，不会被自己误拒。判定失败走回滚。回待办/进完成只减不增负责人计数。

### 3.3 加急：一个全局指针 + 卡上标记双写

- `expedite=true` 仅当目标列是进行中列，否则参数非法；
- 已持标记的卡在进行中列间移动可“携带”标记（不报加急已占用）；
- 标记在卡片离开进行中区域（回待办或进完成）时清除并腾空指针；
- 加急只豁免列上限与负责人上限，依赖与相邻列规则照常判定。

### 3.4 依赖：离开待办时只遍历自己的前置

`Move` 的前置检查 `prereqIncomplete` 只迭代 `card.prereqs`，
因此 Move 复杂度为 `O(该卡前置数)`，与总卡数、依赖图总边数无关。
反向边 `successors` 只在 `Reopen`（检查后继是否仍活跃）与
`AddDep`（成环 DFS）时使用，这两处允许与图大小相关。

### 3.5 下调上限不驱逐、不回补

`SetColumnLimit` 只改 `wipLimit`。已在列内的卡片即使超额也保留；
此后每次移入都按新上限判定。不变量校验因此**不**把“既有超额”当违规，
移入是否超收完全由差分测试对照朴素模型保证。

### 3.6 时钟只在接受时推进

`now < 0 || now > 10^12` 属参数非法（最先判定）；
`now < lastNow` 属时钟回退（第二顺位）。任何拒绝都不改 `lastNow`。

### 3.7 版本号

新建为 1；成功的 Move / Reopen / ChangeOwner / AddDep / RemoveDep 各 +1；
`SetColumnLimit` 是全局配置操作，不影响卡片版本。`expectVer` 不一致即冲突，
冲突判定早于流转/依赖/加急/容量，避免用过期请求触发副作用。

## 4. 被放弃（或刻意不采用）的方案

1. **按列分片锁 / 每卡 CAS 乐观锁**：能提高吞吐，但要在列间转移、
   全局唯一加急槽、全局负责人计数之间做多锁排序，死锁与“部分可见”风险大；
   本规格强调可复现的接受/拒绝语义而非吞吐，单锁更可证。乐观版本仍通过
   `expectVer` 暴露给调用方，拖拽失败后重读版本重试即可。
2. **Move 时全量扫描计数**：实现简单且不易错，但违反
   「Move 开销不随卡片总数增长」的硬性性能要求，故仅保留在朴素模型里做对照。
3. **依赖用 DAG 库/拓扑序维护**：增量维护拓扑序在加边/删边时复杂，
   且规格只允许 AddDep/Reopen 与图大小相关，迭代式 DFS 已足够并可避免深递归。
4. **加急标记仅存卡上、判定时全表扫描**：会让 Move 退化为 O(卡片数)，
   故额外维护 `expeditedCard` 指针；两处一致性由不变量测试守护。
5. **Reopen 接受 expedite 参数**：规格只在 Move 中定义加急入口，
   Reopen 一律走普通容量判定，避免出现第二条加急授予路径。

## 5. 并发模型

- `Service` 的每个方法：`加锁 → Board 纯逻辑 → 解锁 → 记日志`。
- 日志对外部 `io.Writer` 的写入在 `logMu` 临界区内完成，
  因此调用方传入普通 `bytes.Buffer` 也是数据竞争安全的。
- `GetCard` 提供锁内版本读取，供并发客户端实现
  「读版本 → 提交 → 冲突则重读重试」的乐观拖拽循环
  （见 `TestConcurrentDrains`）。

## 6. 性能论证

Move 的工作集：

1. map 取卡、字段比较：O(1)；
2. 前置完成性检查：O(|该卡 prereqs|)；
3. 加急槽判定：O(1)；
4. 列/负责人容量：常数次计数读写；
5. 快照中排序前置：O(|prereqs| log |prereqs|)（仅为输出可读，
   不属于判定路径；判定本身是线性遍历）。

不触碰 `successors`、不遍历 `cards`，因此与总卡数、依赖图总边数无关。
基准 `BenchmarkMoveIndependence`（20 个前置，目标卡在待办↔进行中反复）：

```
BenchmarkMoveIndependence/100cards-20      100000   ~3.2 µs/op
BenchmarkMoveIndependence/100000cards-20   100000   ~2.6 µs/op
```

卡片数增长 1000 倍耗时不增长，可作为“不随卡片总数线性增长”的可验证证据。
AddDep/Reopen 允许与图大小相关：AddDep 的 DFS 与 Reopen 的后继扫描
均显式沿 `successors` 遍历。

## 7. 测试与本地验证

| 测试 | 覆盖点 |
| --- | --- |
| `TestNewBoardValidation` | 列数 3–8、上下限、G 范围、0 表示不限 |
| `TestAddCardAndClock` | 非空 ID/负责人、重复 ID、时钟范围与回退 |
| `TestFlowLegality` | 向右仅邻列、向左任意、完成列冻结、越列、已在原列 |
| `TestColumnLimitAtAndBelow` | 恰满与差一、拒绝后版本/时钟不变 |
| `TestOwnerLimitAndReleaseFirst` | G 限制、列间前移先释放、列/负责人错误码可区分 |
| `TestExpediteUniqueAndClear` | 唯一加急、豁免两类上限、移动携带、回待办/进完成清除、参数非法 |
| `TestLowerLimitDoesNotEvict` | 下调不驱逐、之后移入按新上限 |
| `TestDependencyGating` | 前置完成才离开待办、活跃卡不能加未完成前置、重复/自依赖、增删版本 |
| `TestCycleDetection` | 直接环、间接环、拒绝不改版本 |
| `TestReopenBlockedBySuccessor` | 被活跃/完成后继阻挡、回待办后可 reopen、reopen 受列上限 |
| `TestRejectOrderingAdjacentPairs` | 9 类错误每一对相邻类别的先后 |
| `TestChangeOwner` | 进行中改负责人受 G、待办不受、空/相同负责人 |
| `TestInvariantsAfterRandomishSequence` | 手写混合序列下计数自洽 |
| `TestConcurrentDrains` | 40 goroutine `-race` 并发拖拽，最终全部完成且无竞态 |
| `TestDifferential1500` | 1500 组 × 80 个随机操作与 `naive` 逐步比对错误码与全量状态 |
| `BenchmarkMoveIndependence` | 100 vs 100000 卡规模下 Move 耗时对照 |

差分测试每次运行把「输入、双方输出（接受/错误码）、判定原因」写入
`kanban/diff_trace.log`（约 12 万行），可直接复现每一判定。

运行方式：

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache
go test ./...                       # 全量
go test -race ./...                 # 含竞态
go test -run TestDifferential1500 -v ./kanban
go test -bench BenchmarkMoveIndependence -run '^$' ./kanban
go vet ./...
```

## 8. 朴素参考模型的独立性

`naive` 包刻意与主实现分离：

- 不共享任何代码或计数器，容量判定每次全量重扫所有卡片并临时摆放目标卡；
- 加急占用判定全表扫描而非查指针；
- 错误码用另一套常量，仅通过字符串与主实现对齐。

两边共用的只有操作生成器与比对器（测试文件），因此同一份随机序列
若在两套独立推导的实现上得到逐字节一致的结果与最终状态，
可强力排除“同源思维 bug”。
