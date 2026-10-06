# 快递中转场集袋、封袋与拆袋核对设计

## 模块划分

`expresshub` 包只暴露领域类型、可区分错误码与系统入口，内部按职责拆分为：

- `types.go`：输入输出、品类、袋状态、运单在场状态和配置。
- `errors.go`：统一错误类型与稳定错误码。
- `system.go`：集袋、封袋、出场、拆袋四类写入状态机。
- `queries.go`：开放袋、袋详情、运单位置三类只读快照。
- 测试中的 `naive_model_test.go`：独立朴素模型，线性扫描并直接模拟规则，不复用生产索引。

## 状态机

集袋状态按 `开放 -> 已封存 -> 已出场 -> 已拆袋` 单向流转。

- 开放袋可加入；封存后只能出场；出场后只能拆袋；拆袋不可重复。
- 同一目的网点的开放袋 ID 记录在 `openByStation`，因此同一网点至多一个开放袋。
- 全局袋编号保存在 `nextBagID`。编号只在成功新开袋时递增，任何拒绝路径都在分配编号前返回。
- 空袋从不落库；只有准备放入第一件快件时才创建新袋。
- 全局时钟为 `lastTime`，只有被接受的写入操作更新。

## 自动封袋判定

加入快件时先取得该网点的开放袋。以下任一条件成立即封存旧袋并新开一袋：

1. 旧袋已有件数达到 `MaxItems`。
2. 旧袋总重加新件重量超过 `MaxWeightGrams`。
3. 新件为易碎/液体，且袋内已有另一种非普通品类。
4. 当前时刻大于等于首件加入时刻加 `DwellLimitSec`，相等视为到时。

自动封存、开袋和放入在同一个写临界区内完成，因此不会对外暴露“已封存但仍可加入”或“新件尚未入袋”的中间状态。

单件重量大于袋总重上限时直接业务拒绝；该检查位于任何自动封存和开袋之前，不会为不可分拣件消耗袋编号，也不会提前封存原有开放袋。

## 运单生命周期

`active[waybill]` 是场内运单的唯一索引，保存运单当前袋和是否待查。

- 已加入且未拆袋确认：再次加入返回 `business_reject`。
- 拆袋时扫描到：从 `active` 删除，视为离场，之后可重新加入。
- 拆袋时缺失：标记 `pending=true`，仍保留在索引内，不可重新加入。
- 多出扫描件不属于袋内容，不进入 `active`，只在本次差异清单中记录。

差异清单分别由集合差构造，最后按运单号升序排序。重复拆袋在状态机检查处返回 `invalid_state`，不会重新计算或覆盖第一次差异。

## 错误优先级

所有写入按以下顺序检查，前面的错误先返回：

1. `invalid_argument`
2. `clock_rewound`
3. `not_found`
4. `invalid_state`
5. `station_mismatch`
6. `business_reject`（单件超重、重复运单、待查重加）

查询不携带时间、不移动状态；非法查询参数返回 `invalid_argument`。

## 并发与快照

系统使用一个 `sync.RWMutex` 保护所有可变状态：

- 写操作取写锁，将自动封存、开袋、入袋作为单个临界区事务。
- 查询取读锁，并深拷贝袋内快件切片；调用方修改返回值不会影响系统。
- 任一并发执行的结果等价于这些临界区按某个串行顺序执行。

选择单把粗粒度锁而不是每网点一把锁，是为了让全局时钟、全局袋编号和跨袋运单索引的不变量更直接、可审计。当前规则没有可在多锁协议中获得收益的长耗时 I/O；若后续接入持久化或远程扫描，可在仓储层拆分锁。

## 加入判定复杂度

加入快件的判定只访问：

- `active[waybill]`：哈希表查找，期望 O(1)。
- `openByStation[destination]`：哈希表查找，期望 O(1)。
- 当前开放袋：只检查袋内件数、总重和品类集合，扫描长度不超过 `MaxItems` 这一固定配置。

判定不扫描 `bags`，也不扫描历史快件。因此随场内集袋总数、历史快件总数增长，加入判定的额外开销为 O(1)（期望均摊）；品类扫描只受固定袋容量约束。

`BenchmarkDuplicateAddDecision` 在 100、1,000、10,000、100,000 个场内件下重复判定同一运单；`BenchmarkAcceptedAddAfterHistoricalBags` 先构造同等数量已离场历史袋，再执行成功加入。两者耗时不随历史规模呈线性增长，用于本地验证索引没有退化为历史扫描。

## 确定性与可复现

- 袋编号由系统内单调计数器分配。
- 差异清单由集合差生成后排序，不依赖调用方 `Scanned` 的输入顺序。
- 测试使用固定随机种子，重跑相同操作序列得到相同接受/拒绝结果、袋编号、封袋时机和差异清单。
- 并发测试只验证可串行化与快照完整性，不把非确定性线程调度顺序作为业务结果输入。

## 被放弃的方案

- 为每个网点单独加锁：能降低锁竞争，但会让全局时钟、编号和运单跨网点唯一性引入多锁顺序，增加死锁与中间状态风险；当前内存模型不需要这种复杂度。
- 用袋列表线性查找开放袋：实现更朴素，但加入开销随袋数增长，违反性能要求；该做法仅保留在独立测试模型中做对照。
- 将缺失件从场内索引删除并另建待查集合：状态表达更分散，容易与可重新加入的已离场运单混淆；单索引加 `pending` 标记更能保证唯一归属。
- 依赖输入扫描顺序输出差异：实现简单但重放不稳定；统一排序后结果可精确复现。

## API 摘要

```go
system, err := expresshub.New(expresshub.Config{
    MaxItems:       50,
    MaxWeightGrams: 20_000,
    DwellLimitSec:  3600,
})

add, err := system.AddParcel(expresshub.AddParcelInput{
    Waybill:     "SF1001",
    Destination: "SITE-A",
    WeightGrams: 1200,
    Category:    expresshub.CategoryFragile,
    At:          100,
})

sealed, err := system.SealBag(expresshub.SealBagInput{Destination: "SITE-A", At: 120})
_, err = system.DispatchBag(expresshub.DispatchBagInput{BagID: sealed.BagID, VehicleID: "TRUCK-7", At: 200})
result, err := system.VerifyBag(expresshub.VerifyBagInput{
    BagID:       sealed.BagID,
    Destination: "SITE-A",
    Scanned:     []string{"SF1001"},
    At:          500,
})
```

错误可通过 `*expresshub.Error` 的 `Code` 区分：

- `invalid_argument`
- `clock_rewound`
- `not_found`
- `invalid_state`
- `station_mismatch`
- `business_reject`

## 本地验证

```bash
GOCACHE=/tmp/go-build /usr/local/go/bin/go test -v ./...
GOCACHE=/tmp/go-build /usr/local/go/bin/go test -race ./...
GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./...
GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./expresshub -run '^$' -bench BenchmarkDuplicateAddDecision -benchtime=1000x
```

`-v` 会打印随机对照与边界用例的输入、输出和判定原因。随机测试使用 300 个固定种子、每个种子 120 个混合操作，并在每一步后逐袋、逐运单比较生产系统和朴素模型。
