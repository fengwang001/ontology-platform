# 快递智能柜：设计说明

## 1. 模块划分

全部代码在 `locker` 包内，按职责拆成互不越界的文件：

| 文件 | 职责 |
| --- | --- |
| `size.go` | `Size` 三档（序值 小<中<大）、运单号/手机号类型与后四位校验 |
| `config.go` | 固定参数（免费时长、计费周期、单价、封顶、保管上限、冷却、码总数）与校验 |
| `cellindex.go` | 三级层次位图：空闲格口的 O(1) 最小可容纳分配与释放、探测计数 |
| `codepool.go` | 取件码分配/失效/冷却（最小可用码 + 冷却最小堆），保证有效码唯一 |
| `fee.go` | 滞留费纯函数（免费边界、完整周期、封顶） |
| `parcel.go` | 快件运行时状态：欠费 `due` 与超时 `timedOut`（均为时刻纯函数） |
| `cabinet.go` | 并发安全入口：存/取/缴/回收/解锁，集中实现拒绝优先级与时钟语义 |
| `observe.go` | 日志（输入、输出、判定依据）与只读观测接口 |
| `errors.go` | 哨兵错误，调用方用 `errors.Is` 精确区分拒绝原因 |

测试侧：

| 文件 | 内容 |
| --- | --- |
| `boundary_test.go` | 各边界与优先级单测 |
| `concurrency_test.go` | 并发取件恰好一次成功、并发存件无格口共用 |
| `complexity_test.go` | 分配开销不随规模增长的**可观测证明** |
| `model_test.go` | 独立编写的朴素参考模型（线性扫描选格口、1..N 扫码） |
| `diff_test.go` | 生产实现 vs 朴素模型的大量随机差分（400 序列 × 300 操作） |

## 2. 关键取舍

### 2.1 格口分配：三级层次位图，而非最小堆

每档一张三级位图：`leaf[]`（64 位/字，1 位=1 格口）→ `l1[]`（每 64 个 leaf 字
汇总成 1 位）→ `l2`（每 64 个 l1 字汇总成 1 位）。单档上限 64³=262144 格口。

取「编号最小的空闲格口」固定为：`TrailingZeros(l2)` → 定位 l1 字 →
`TrailingZeros(l1)` → 定位 leaf 字 → `TrailingZeros(leaf)`，共 **3 次机器字访问**。
存件从快件规格那档向上逐档探测（空档 1 次、命中档 3 次），
**任何情形 ≤ 9 次字访问**，与格口总数、历史快件数均无关。

被放弃的方案：

- *每档一个最小堆*：释放时要把格口重新入堆，可行，但堆操作是 O(log n)，
  且常数大于 3 次字访问；位图更直接地给出「最小」与「非空」。
- *线性扫描数组*：实现最简单（朴素模型就是这么写的），但 O(格口数)，
  明确被需求排除，仅保留作差分对照。

### 2.2 开销的可观测证明

不是只在文档里声称 O(1)，而是让复杂度可测量：`CellIndex` 对每次机器字访问计数，
经 `Cabinet.Stats()/ResetStats()` 暴露 `WordProbes`。
`TestAllocationProbeBoundedAcrossScales` 在 63…100000 个格口的规模下重放同一形态
序列，断言每次分配的平均探测数恒定为 3.000 且永不超过 9；
`TestProbeBoundUnderMultiTierScan` 在 10/1000/20000 格口/档时让小件跨档到大格口，
探测数恒为 5（空小档 1 + 空中档 1 + 命中大档 3）。日志中会打印各规模实测值。

### 2.3 取件码：固定码池 + 最小可用码 + 冷却堆

- 码是 1..N 的整数序号。空闲码放最小堆，冷却码放 `(可再用时刻, 码)` 最小堆。
- 分配前先把到期时刻 `<= now` 的冷却码移回空闲堆（`<=` 即「恰好满冷却即可再用」），
  再取序号最小者——因此**相同操作序列重放必然得到相同取件码序列**。
- 有效码唯一性：`active` 集合 + 分配即移出两个堆，结构上不可能重码。
- 冷却堆只在分配时惰性到期；冷却项数量 ≤ 码总数，与历史快件数无关。

被放弃的方案：用「历史最大码 +1」生成新码（满足唯一性但违反失效后复用，
且会随历史增长）；用随机码（违反确定性重放）。

### 2.4 费用与超时：时刻纯函数

```
elapsed = now - depositedAt
elapsed <= FreeStorage                 -> 0     // 恰好免费时长也为 0
fee = (elapsed - FreeStorage)/Period * Unit
fee = min(fee, FeeCap)
timedOut = (now - depositedAt) >= MaxStorage     // 恰好满即超时
```

不存在后台定时器：超时不触发任何动作，只是取件/回收在该时刻读到的判定结果。
缴费记到快件的 `paid` 上；欠费 `due = max(0, fee(now) - paid)`。
时间继续推进产生新欠费时，旧缴费继续有效，只需补缴增量。
多缴作为预付留用（不退款，不产生负欠费）。

### 2.5 时钟与拒绝的状态语义

- `clock` = 最近一次**被接受**操作的时刻；`t < clock` 报 `ErrClockRollback`。
- 被拒绝操作不改任何状态、不推进时钟；**唯一例外**是取件手机号后四位不符：
  累加该快件错误计数（满 3 次置锁定），但仍不推进时钟、不改其他任何东西。
- 因此第三次错误把件锁死后，下一次即使手机号正确也只报「已锁定」
  （锁定检查在手机号检查之前）。运营 `Unlock` 清锁并清零计数；成功取件也清零。

### 2.6 拒绝优先级（精确复现的核心）

- 存件：参数非法 → 时钟回退 → 重复运单 → **柜内无足够规格格口**
  → 足够格口全占用 → 无可用取件码。
  「根本没有」先于「全占用」：先看该规格以上是否存在格口（`HasFitting`），
  再尝试分配。无码可用时回滚已分配的格口，保持其空闲。
- 取件：参数非法 → 时钟回退 → 码不存在/已失效 → 已锁定
  → 手机号不符（计数但不走钟）→ 已超时 → 待缴费。
- 缴费：参数非法 → 时钟回退 → 运单不存在。
- 回收：参数非法 → 时钟回退 → 运单不存在 → 未超时。
- 解锁：参数非法 → 时钟回退 → 运单不存在。

### 2.7 并发：单把互斥锁串行化

所有公开方法进入即取 `sync.Mutex`。这直接给出「结果等价于某个串行顺序」；
同一取件码并发取件时，成功者在锁内把码从 `byCode` 删除，后来者必读到
`ErrCodeNotFound`，故恰好一次成功。格口位图与码池都在同一把锁内变更，
结构上排除格口共用与有效码重复。相较细粒度锁/无锁结构，单锁更易论证正确，
而格口分配本身只有常数次字访问，锁内临界区极短。`TextLogger` 自带写锁，
因此操作日志本身也并发安全。

## 3. 本地验证方法

需要 Go 1.26+（本机位于 `/usr/local/go/bin`）。

```bash
# 全量测试 + 竞态检测
PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache go test -race ./...

# 详细查看边界判定与规模探测数
PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache go test -v ./locker

# 只跑随机差分（生产实现 vs 朴素模型，12 万次随机操作）
PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache go test -v -run TestNaiveModelDifferential ./locker

# 静态检查与格式化
PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache go vet ./...
PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache gofmt -l .

# 业务闭环演示（打印每个操作的输入/输出/判定依据）
PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache go run ./cmd/locker-demo
```

测试覆盖与需求对应：

- 规格恰好相等 / 差一档：`TestCellSelectionExactSizeAndOneTierUp`
- 「无足够格口」优先于「全占用」：`TestNoFittingCellPrecedesBusy`
- 冷却恰到（t=失效+冷却 可用）/ 差一秒：`TestCodeCooldownExactAndOneSecondBefore`
- 免费时长与计费周期边界：`TestFeeBoundaries`
- 保管上限恰到（满即超时）/ 差一秒：`TestTimeoutBoundary`、`TestRecycleOnlyTimedOutAndCellReuse`
- 三次错误锁定、正确号也拒、解锁、成功清零：`TestThreeMismatchesLockAndUnlock`、
  `TestTwoMismatchesThenSuccessResets`
- 拒绝不改状态/时钟 + 错误计数例外：`TestRejectedOpsDoNotMutate`、
  `TestDuplicateAndClockRollback`、锁定测试中的 clock 断言
- 缴费后时间推进再补缴：`TestPayAccruesAndTopUp`
- 并发取件恰好一次、并发存件无格口共用：`concurrency_test.go`（`-race`）
- 大规模随机序列与朴素模型对照：`TestNaiveModelDifferential`

## 4. 明确的语义约定（需求留白处）

- 手机号只使用末四位；末四位必须为 4 个数字，否则属参数非法。
- 运单号为空、时刻为负、规格越界、缴费金额 ≤ 0、取件码 ≤ 0，均参数非法。
- 缴费可对锁定件/超时件进行（缴费本身不解锁，也不使超时件可取）。
- 回收与取件一样使格口立即空闲、取件码进入冷却；运单号随后可再次投递。
- 码总数不足时存件报 `ErrNoCodeAvailable`（一种独立的拒绝原因）。
