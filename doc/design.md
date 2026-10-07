# 链接实例层：创建/去重仲裁设计说明

本文档说明本体平台中“两个对象类型之间的链接类型”在实例层的创建、去重、
基数仲裁设计，覆盖双方向基数、重复声明、撤销后重占、并发确定性、
失败分类、历史无关的计数以及跨链接类型隔离。

代码位于 `ontology/` 包，无任何外部依赖。

## 1. 模型与术语

- `ObjectType`：对象类型声明（如 Person、Org）。
- `Object`：对象实例，带逻辑删除位 `deleted`。
- `LinkType`：在 `sourceType -> targetType` 上声明的链接类型，含
  - 两个方向各自独立的基数 `forwardCap` / `backwardCap`（可取 `AtMost(n)`，
    `n` 可以是 0；或 `Unlimited()`，两侧可不同）；
  - 一组**有序**区分属性名 `discrimAttrs`。
- `Direction`：`Forward = source->target`，`Backward = target->source`。
- `Link`：一条已登记链接，拥有平台单调分配的实例 ID 与独立的派生状态
  （以 `annotations` 为代表）。

### 基数语义（关键取舍）

每个方向的上限约束的是：**该方向上、以某个实例为“尾（tail）”的已登记
链接数**。即计数桶的键为

```
countKey = (linkTypeID, direction, tailInstanceID)
```

这一选择使两个方向天然独立：在 Forward 上被拒绝完全不会触碰
`(lt, Backward, head)` 桶，也不会触碰另一侧实例的桶。它也能直接表达
常见的一对一 / 一对多 / 多对多：声明侧（Person 为尾）给 `AtMost(N)`，
反方向（Org 为尾）给需要的上限即可。

被放弃的替代方案：用“一个桶同时约束两端参与数”的对称计数。该方案在
双向上限不同（如一对多）时必须在一个临界区里同时检查并修改两个耦合
计数器，且一个方向拒绝时需要回滚对另一侧的预占，语义与实现都更复杂，
也更难证明“拒绝不影响另一侧判断”。

## 2. Store 的数据结构

所有判定与变更都集中在 `Store` 的一把 `sync.Mutex` 之下：

| 结构 | 键 | 含义 |
| --- | --- | --- |
| `links map[linkKey]*Link` | `(链接类型, 方向, 尾, 头, 规范化区分属性串)` | 当前有效链接 |
| `byID map[string]*Link` | 链接实例 ID | 供删除/注解按 ID 定位 |
| `counts map[countKey]int` | `(链接类型, 方向, 尾实例)` | 当前计数，归零即删桶 |
| `audit []DecisionRecord` | 单调 `Seq` | 输入、判定依据、结果的全序日志 |

链接身份键 `linkKey` 中，区分属性组合按链接类型声明的属性名顺序
（再对额外属性名排序）做确定性序列化，并对 `&`、`=`、`%` 做百分号转义，
因此 map 提供顺序不同不会影响重复判定，属性值里的分隔符也不会破坏规范性。

撤销是**物理删除**：同时从 `links`、`byID` 移除，计数减一（到 0 删桶）。
系统不在任何参与判定或计数的结构中保留已撤销链接。

## 3. 创建仲裁的固定判定顺序

`CreateLink` 持锁后依次做四步检查，任何一步失败都在任何写操作之前返回：

1. **实例存在性**：尾、头实例都已登记且未逻辑删除，否则
   `object_not_found`。该类别固定优先于适用性、重复性、基数，
   即使后三者同时成立也只返回它。
2. **链接类型与方向适用性**：链接类型已注册，且该方向允许
   `尾类型 -> 头类型`，否则 `link_type_not_allowed`
   （未知类型为 `link_type_not_found`）。
3. **重复判定**：`linkKey` 已在 `links` 中 => `duplicate_link`，
   返回在库链接且**不修改计数**，因此重复声明不占基数名额。
4. **目标方向基数**：`counts[ck] >= cap` => `cardinality_full`。

只有全部通过才：分配新 ID → 写两个索引 → 计数加一 → 追加 accepted 审计。

被放弃的替代方案：“先检查基数再检查重复”。在 cap 已满且请求又是重复
时，那会把幂等的重声明错误地报成“已满”，破坏“重复不占名额”的直觉。
本实现固定“重复优先于基数”（对象不存在仍然最优先），并由
`TestStableFailurePrecedence` 保证同输入稳定同结果。

## 4. 撤销、重占与派生状态隔离

- `DeleteLink(id)`：从活跃索引物理移除并立即释放计数名额；
  重复删除返回 `link_not_found`，同样无任何副作用。
- 同一区分属性组合在撤销后可以立即被新请求重占；但新链接获得全新 ID
  与全新（空）的派生状态。旧链接的 `annotations` 等随旧实例一起消失，
  不做任何拷贝。`TestRevokedTupleReusableWithoutDerivedState` 验证
  “旧 ID 不可读、新 ID 读不到旧值、新旧 ID 三者互不相同”。
- 对象逻辑删除只翻转 `deleted` 位：之后引用该实例的创建按
  `object_not_found` 失败；已存在的链接不级联撤销，仍需逐条显式撤销
  （取舍：避免对象删除意外释放基数名额、制造难以对拍的隐式移动）。

## 5. 并发：可线性化与删除/创建竞态

所有变更方法在同一把互斥锁内完成“检查 + 写入 + 审计追加”。因此：

- “当前未超限”不可能被两个请求同时观察到：观察与自增是同一个临界区，
  实际登记数不可能超过声明上限；
- 并发的删除与同键创建只有两种串行序：
  - 删在前：名额先释放，创建成功，新链接占位；
  - 建在前：在旧链接仍在时创建，因重复被拒或因不同组合进入基数判断，
    删除随后再释放名额。
  不会出现“删除生效但名额未释放”或“新建抢到尚未释放的名额”这类
  不对应任何串行序的状态。

审计日志的 `Seq` 就是这把锁给出的全序，可直接当作线性化顺序使用。

被放弃的替代方案：

- *每桶独立锁/无锁 CAS 计数*：删除与同键创建横跨“重复索引 + 计数桶”
  两个结构，细粒度锁需要额外的合并锁来避免“抢到未释放名额”，收益小、
  证明复杂；当前规模下单锁更可靠（所有操作均为内存哈希访问，临界区极短）。
- “软删除 + 墓碑去重”：会让重复判定和计数都必须永久扫描历史，直接违背
  “计数成本只与当前有效链接相关”，故放弃。

## 6. 失败分类

失败以 `*DecisionError` 暴露，调用方用 `Code()` 区分，禁止合并：

| 代码 | 含义 |
| --- | --- |
| `object_not_found` | 引用实例不存在或已逻辑删除（最高优先） |
| `link_type_not_found` | 链接类型未注册 |
| `link_type_not_allowed` | 该链接类型不允许此对象类型对/方向 |
| `duplicate_link` | 区分属性组合与在库链接冲突 |
| `cardinality_full` | 目标方向基数已满 |
| `link_not_found` | 删除/注解目标不在库（含已撤销） |

每个错误都带 `Basis()`（判定依据列表），与审计记录一一对应。

## 7. 审计

每次创建/删除（含逻辑删除对象）都追加一条 `DecisionRecord`：
`Seq`（严格递增全序）、`Operation`、人类可读 `Input`、结构化
`CreateInput`/`TargetID`（供离线回放）、`Basis`、`Result`、`LinkID`。
被拒绝的请求同样记录，但只追加日志——日志不被任何判定路径读取，
因此失败请求对后续查询零影响。

## 8. 历史无关的计数（可验证）

`CountLinks` 只做一次 `counts` 哈希查表，且 `counts` 中不含任何已撤销
链接（撤销即减计数、归零删桶）。因此其开销只与“当前仍有出链的尾实例
数量”相关，与历史总量无关。

验证方式有两种：

1. 结构断言 `TestCountIndependentOfHistory`：在同一对实例上反复
   创建/撤销 2000 条、最终只留 3 条，断言 `len(links)==3`、
   `len(byID)==3`、`len(counts)==1` 且桶值为 3，并与朴素模型对拍。
2. 基准（本机结果）：

```text
BenchmarkCountSmallHistory-20   23437011   44.26 ns/op   0 B/op   0 allocs/op
BenchmarkCountLargeHistory-20   27256303   44.64 ns/op   0 B/op   0 allocs/op
```

历史量扩大 100 倍（200 -> 20000），单次计数耗时基本不变且零分配。
复跑：`go test -bench=BenchmarkCount -benchmem ./ontology/`。

## 9. 跨链接类型隔离

`linkKey` 与 `countKey` 都以 `linkTypeID` 为首字段。多个链接类型作用于
同一对实例时：

- 基数桶互不相交，一个类型的拒绝/撤销不改变另一个类型的计数；
- 重复判定只在本类型的 `links` 子集中匹配，相同区分属性值不会跨类型
  互相抑制。

由 `TestLinkTypesIndependent` 与并发对拍中的双链接类型场景共同覆盖。

## 10. 朴素参考模型与并发对拍

`NaiveStore`（`ontology/naive.go`）是独立编写的无锁、线性扫描参考实现：
不共享 `Store` 的任何数据结构，重复与基数都靠全量扫描当前活跃切片。

`TestConcurrentCreateDeleteVsNaive` 每轮用固定种子生成 400 个操作
（创建约 65%、删除约 32%、逻辑删除对象约 3%；含两个链接类型、双方向、
相同区分组合的反复创建/删除/重建），由多个 goroutine 随机交织执行；
跑完后：

1. 取 `Store` 的审计全序，在一个全新 `NaiveStore` 上按序回放，逐笔比较
   结果码（含所有拒绝类别），删除按“store ID -> naive ID”映射转发；
2. 比较两个实现最终在库链接内容多重集（签名不含实例 ID，因为重占必然
   产生新 ID）；
3. 比较所有 `(链接类型,方向,尾实例)` 计数。

共 30 个不同随机种子，且 CI 可用 `-race -count=N` 复跑。

## 11. 本地验证方法

```bash
go test ./...                                   # 全量测试
go test -race -count=3 ./...                    # 竞态 + 重复
go test -run TestCardinalityBoundaryMatrix -v ./ontology/
go test -run TestConcurrentCreateDeleteVsNaive -v ./ontology/
go test -bench=BenchmarkCount -benchmem ./ontology/
go vet ./... && gofmt -l .
```
