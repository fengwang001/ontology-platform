# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前模块 `ontology` 实现**分区级数据资产的过期判定与回填规划器**：
给定资产、分区范围与带偏移的依赖边，判定分区是否过期、规划最小回填集合、
给出重写影响面。任何时刻的判定都等价于“按定义逐分区重算”的结果。

## 模型

- 资产按整数日序号分区，声明范围 `[First, Last]`。
- 依赖边 `Up -> Down`，偏移闭区间 `[Lo, Hi]`：`Down` 的分区 `d` 物化时读取
  `Up` 的分区 `[d+Lo, d+Hi]`；落在 `Up` 声明范围外的分区忽略（区间与范围取交集）。
- 无入边的资产为**源**，只能通过 `ExternalWrite` 写入；其余为派生资产。

### 版本、消费记录与物化

- 源分区每次外部写入版本加一；首次写入即视为已物化。
- 派生分区通过 `Start` / `Complete` 物化：
  - `Start` 在**开始时刻**记录全部输入分区“此刻”的版本（消费快照）。
  - `Complete(success=true)` 时分区版本加一，并把该快照固化为该分区的
    **消费版本记录**。
  - `Complete(success=false)` 不改变任何状态（运行结束但分区仍缺失/旧版本）。
  - 同一分区已有运行中的物化时，再次 `Start` 被拒绝。
- 开始物化要求每个输入分区都**已物化且不过期**；未就绪时一次性列出全部
  缺失或过期输入。

### 过期定义

设分区 `p` 已物化。`p` **过期**当且仅当它的某个输入分区 `q` 满足：

1. `p` 的消费记录中 `q` 的版本 `≠ q` 的当前版本（输入被重写）；或
2. `q` 自身过期（过期沿物化分区递归传播）。

未物化分区为**缺失**，不是过期；缺失分区不参与过期递归。源分区只要存在就
永远不算“自身过期”，但源被重写后，消费过旧版本的下游会因第 1 条过期，
而该过期下游本身又会阻塞它的下游开始（第 2 条）。

注意物化结果可能“一落地即过期”：若运行期间输入被改写，`Complete` 成功后
立即用完成时刻的快照判定，该分区即刻过期并进入回填规划。

### 回填规划 `Plan`

输入一组目标分区，从目标出发沿依赖边遍历其**全部传递输入**；收集其中
“缺失或过期”的分区。新鲜分区（包括新鲜的源）不进入结果。因此结果即
“让目标变新鲜所需物化的最小分区集合”，按
`(资产层深, 资产名, 分区号)` 升序返回。

层深定义：源为 `0`；其余资产为其所有上游层深最大值加 `1`（建图时无环，
一次记忆化 DFS 即可求得）。

### 影响面 `Impact`

给定分区 `x`，求“`x` 若被重写，将传递变为过期的已物化下游分区”：

- 维护反向消费索引：输入分区 → 已物化并读取它的下游分区集合（仅在
  `Complete` 成功时按当次消费快照登记）。
- 从 `x` 做传递闭包；只有已物化、且消费记录中含该上游的下游才会受影响，
  未物化的中间层不传播。结果同样按 `(层深, 资产名, 分区号)` 升序，不含
  `x` 自身。

负偏移与跨度大于一的边按真实闭区间连接（先与上游声明范围求交），影响面
方向与读取方向一致。

## 拒绝原因

所有错误均为 `*ontology.Error`，其 `Reason` 可区分：

- 建图（按此优先级）：`duplicate_asset`、`unknown_asset`、`duplicate_edge`、
  `dependency_cycle`（含自依赖与多节点环）、`lo_greater_than_hi`、
  `first_greater_than_last`。
- 操作（按此优先级）：`unknown_asset`、`partition_outside_range`、
  `already_running`、`input_not_ready`（`Details["inputs"]` 给出全部
  `NotReadyInput`，标明 `Missing`）、`run_not_found`、`run_already_finished`、
  `external_write_on_non_source`。

任何被拒绝的操作都不改变状态（包括不惰性创建分区记录）。

## 并发与一致性

- 规划器内部使用单一读写锁，`Start`/`Complete`/`ExternalWrite` 串行化提交，
  `Plan`/`Impact` 与只读判定共享读锁。
- 每次判定在一次加锁期间完成，基于**一致快照**；因此规划结果必与某个串行
  操作顺序一致。同一分区并发 `Start` 恰好成功一次，其余得到
  `already_running`。

## 日志

默认输出到 stderr，可用 `ontology.WithLogOutput(io.Writer)` 重定向。
每次操作记录输入、输出与判定依据，例如：

```text
ontology ... start OK asset=A part=1 run=2 depth=1 inputs=S#1@2,S2#0@1
ontology ... complete OK run=2 asset=A part=1 success=true newVersion=2 staleAtBirth=true why=input S#1 version 2 != consumed 1
ontology ... plan OK targets=[B#1] result=[A#1,B#1] basis=[A#1=stale(input S#1 version 2 != consumed 1)]
ontology ... impact OK root=S#1 result=[A#1,B#1]
ontology ... start REJECT asset=D part=1 reason=input_not_ready inputs=M#1(stale)
```

## 快速上手

```go
p, err := ontology.New(
    []ontology.AssetSpec{
        {Name: "events", First: 0, Last: 365},
        {Name: "daily_agg", First: 0, Last: 365},
    },
    []ontology.EdgeSpec{
        {Up: "events", Down: "daily_agg", Lo: 0, Hi: 0},
    })

_ = p.ExternalWrite("events", 7)
run, _ := p.Start("daily_agg", 7)
_ = p.Complete(run, true)

need, _ := p.Plan([]ontology.PartitionRef{{Asset: "daily_agg", Partition: 7}})
affected, _ := p.Impact("events", 7)
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 本地验证

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出（含并发开始、混合并发用例）
go test -race -v ./...

# 重复运行确认稳定
go test -race -count=10 ./...

# 代码检查
gofmt -l .
go vet ./...
```

测试覆盖：运行期间输入被改写导致结果一落地即过期、负偏移与跨度大于一的
影响面方向与范围裁剪、输入（中间层）过期导致下游不可开始且列出全部未就绪
输入、规划不含新鲜分区、层深排序、缺失闭包、失败完成不改变状态、各类建图
与操作非法输入的拒绝原因、被拒绝操作不落任何状态、同一分区并发开始只成功
一次，以及日志中的输入/输出/判定依据。
