# 属性索引增量维护子系统 — 设计说明

代码位于 `ontology/`（包 `ontologyindex`），演示入口 `cmd/indexdemo`。

## 1. 要解决的问题

- 消费对象属性变更流，维护“按属性取值定位对象”的倒排索引。
- 事件可能**乱序**到达（高时间戳先到、低时间戳后到）、可能**重复**投递。
- 索引依据字段会随类型定义版本迁移而**改名/替换**或**废弃**；切换必须
  原子，查询端不得看到新旧依据混杂。
- 校验失败必须整体回滚，切换窗口内缓冲的事件不得丢失。
- 并发的维护、切换、查询必须可串行化。
- 查询开销不得随累计事件数线性增长。

## 2. 关键概念与取舍

### 2.1 合法串行顺序只由逻辑时间戳决定

每条 `ChangeEvent` 携带：

- `EffectiveAt`：业务世界的**逻辑生效时刻**（单调有序的逻辑时钟）；
- `EventID`：全局唯一，用于精确去重与同刻决胜；
- `ArrivedAt`：物理到达序号，**仅用于审计，绝不参与顺序判定**。

合法串行顺序定义为：

```
先按 EffectiveAt 升序；EffectiveAt 相同则按 EventID 字典序升序。
```

因此无论事件以什么物理顺序到达，同一对象同一属性的最终取值唯一确定。
重复事件（相同 `EventID`）幂等忽略，不会二次覆盖。

被放弃的方案：

- *按到达顺序覆盖*：无法处理乱序，直接违背需求；
- *向量时钟/混合时钟*：对“单一事实流”过重，且需要额外的时钟协调假设。
  本设计要求上游为每条变更分配可比较的逻辑时间戳（等价于 Kafka 分区
  偏移、事务提交序列号或版本号），这是事件溯源系统通常已具备的。

### 2.2 逻辑属性身份与物理字段分离

- 类型的每个版本（`TypeVersion`，带 `EffectiveAt` 区间）定义若干
  `FieldVersion`：物理字段名 `Physical` 可能随迁移改变，但
  `PropertyID` 是跨版本稳定的逻辑属性身份。
- 迁移通过 `Deprecated + ReplacedBy` 表达替代链：
  `ssn(废弃) -> tax_id`。事件只引用 `PropertyID`，由
  `Schema.resolveProperty` 在事件**生效时刻适用的类型版本**上沿链解析。
- 一条索引绑定的是逻辑属性血缘（`Schema.lineageOf`），因此 `ssn` 与
  `tax_id` 上的事件自动归入同一条索引。

### 2.3 版本归属的确定规则（跨越切换点的事件）

切换由 `BeginSwitch(indexID, cutoverAt)` 宣布一个**确定的逻辑时刻**，
归属规则不看物理到达时刻，只看事件的 `EffectiveAt`：

| 事件 EffectiveAt | 物理到达时机 | 归属 |
|---|---|---|
| `< cutoverAt`（旧字段） | BeginSwitch 之前/之中/Commit 之后 | 旧版本（提交后到达的仅入历史日志，不污染新版本） |
| `>= cutoverAt`（新字段） | BeginSwitch 之前/之中 | 新版本（提前到达也只是缓冲） |

实现上，切换窗口（`switching` 相位）内**不增量修改任何活跃版本**：
所有合法事件只写入每索引的全量事实日志 `log`。提交时在隔离副本上
从日志重放构建新版本，校验通过后一次性替换活跃指针；失败则用同一日志
重放旧版本。查询在 `switching` 相位直接返回 `ErrSwitchInProgress`，
因此查询端只有三态：完整旧版本、不可查（切换中）、完整新版本，
从机制上排除了新旧依据混杂。

### 2.4 E2 失败回滚不丢事件

事件日志 `log` 是唯一事实来源，状态机的每个物化版本都可由它确定性
重建（`materialize`）。`CommitSwitch` 校验失败或 `AbortSwitch` 时：

1. 丢弃未发布的候选版本；
2. 用同一份 `log` 重放“覆盖全部事件”的旧版本物化；
3. 切换窗口中缓冲的新、旧字段事件因此全部**重新归入旧版本继续生效**，
   按 `(EffectiveAt, EventID)` 决定最终值，不会因为切换失败而丢失。

这一“回滚即从日志重放旧版本”的取舍，放弃了更复杂的“新旧双写 +
反向补偿”方案，换取了确定、可审计、易与朴素模型对照的语义。

### 2.5 错误分类与优先级

四类互可区分的错误（`errors.go`）：

| Code | 触发条件 |
|---|---|
| `ErrDeprecatedProperty` (E1) | 索引依据属性沿替代链走到“废弃且无替代” |
| `ErrSwitchValidation` (E2) | 新版本物化违反索引约束（如唯一约束） |
| `ErrPropertyUndefined` (E3) | 事件在其 `EffectiveAt` 适用的类型版本中引用未定义属性 |
| `ErrSwitchInProgress` (E4) | 查询发生在 `switching` 相位 |

同一次判定同时满足多类条件时，优先级为
`E1 > E2 > E3 > E4`，由 `highestPriorityError` 只返回最高优先级一类。
任一错误发生时状态机都不发生部分提交（校验在隔离副本上完成），
因此不会留下新旧混杂状态。

### 2.6 并发模型：单一互斥锁线性化

`Engine` 用一把 `sync.RWMutex` 把所有操作线性化：

- `Ingest / BeginSwitch / CommitSwitch / AbortSwitch / DeprecateIndex`
  取写锁；`Lookup / Status` 取读锁。
- 版本发布是“构建新物化 → 校验 → 指针替换”，替换动作在锁内原子完成。

这给出严格的可串行化（事实上是线性一致）语义，且实现简单、不易出
活锁/漏窗。被放弃的方案：

- *分片锁*：切换操作天然横跨所有对象分片，需要额外的同步屏障，
  复杂度高且收益依赖极高写入吞吐；
- *无锁原子指针 + 后台构建*：查询端虽可实现无暂停，但切换校验失败时
  的缓冲/回收与内存屏障推理复杂。当前实现用短暂的“切换中查询 E4”
  换取了强一致与可证明正确，符合需求中允许在切换瞬间显式报错的约定。

### 2.7 查询复杂度

物化结果是双向投影：

- `valueToObjects: Value -> 有序对象切片`（倒排表）
- `objectToValue / objectToTS / objectToEvent: ObjectID -> ...`

`Lookup` 只做一次哈希定位并拷贝结果切片，**不扫描事件日志，也不在
查询路径排序**（排序在增量写入时由 `sortedObjectSet` 维护）。
平均复杂度 `O(1 + k)`，`k` 为命中对象数，与累计事件总数 `N` 无关。

独立验证方式（见 `ontology/benchmark_test.go`）：

- `go test -bench=BenchmarkLookup -benchmem ./ontology`
  提供 1k/10k/100k/1m 四档基准；
- `ONT_RUN_SLOW=1 go test -run TestLookupConstantTime ./ontology`
  自动比较 1 万与 100 万累计事件下的最小平均单次查询耗时，线性算法在
  100 倍数据跨度下应接近 100 倍耗时，而实测比值远低于阈值（见下）。

实测（arm64，单命中桶，把定位开销与结果拷贝分离）：

```
BenchmarkLookupSingleHit1k     310 ns/op   352 B/op  3 allocs/op
BenchmarkLookupSingleHit100k   269 ns/op   352 B/op  3 allocs/op
BenchmarkLookupSingleHit1m     202 ns/op   352 B/op  3 allocs/op
```

事件总量增长 1000 倍，单次查询的耗时与内存分配基本恒定，直接验证
“按取值定位”的步数不依赖累计事件数；返回大结果集时的额外分配只与
命中对象数 k 有关（见 `BenchmarkLookup1k..1m`）。

## 3. 被放弃方案汇总

1. 物理到达顺序决定最终值 —— 乱序下语义错误。
2. 事件直接引用物理字段名 —— 字段改名后无法把新旧事件归为同一逻辑属性。
3. 切换窗口内对活跃版本双写/增量打补丁 —— 易产生混杂与补偿漏洞；
   改为“窗口内只缓冲，提交/回滚统一从日志重建”。
4. 分片锁 / 无锁 CAS 发布 —— 在本需求下复杂且收益不明确，改用
   单把读写锁获得可证明的线性一致。
5. 查询时再对结果排序 —— 曾导致查询随命中规模变慢（基准中暴露，
   1M 事件时单次约 290ms），改为增量维护有序集合。

## 4. 本地验证方法

```bash
# 常规测试（乱序/重复穷举、跨切换归属、回滚、四类错误、差分、并发）
go test -race -count=1 ./...

# 查询复杂度自动断言（构建百万级事件，较慢）
ONT_RUN_SLOW=1 go test -run TestLookupConstantTime -v ./ontology

# 四档查询基准
go test -bench=BenchmarkLookup -benchmem ./ontology

# 端到端演示
go run ./cmd/indexdemo
```

审计：`MemoryAuditor`（内存，供断言）与 `JSONLAuditor`（JSONL 落盘，
供事后核查）实现同一 `Auditor` 接口；每条判定记录操作、事件输入、
物理到达序号与逻辑生效时刻、所依据的 `IndexID/IndexVersion`、
判定结论与错误码。
