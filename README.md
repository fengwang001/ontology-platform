# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

本仓库当前包含 `ontology` 包：一个在**表结构演进**下解码**历史版本事件**的组件。
它能按列的**稳定标识**，把任意旧版本写入的一行事件解码成**当前最新结构**下的一行，
并在列的增、删、改（改名）场景下始终给出正确结果。

## 环境要求

- Go 1.26+（`go version` 确认）

## 核心模型：列标识（Stable Column ID）

- 每列有三个属性：稳定标识 `ColumnID`、名字 `Name`、默认值 `Default`。
- **标识一经分配，永不复用**：
  - 新增列一律在**末尾追加**，并分配一个单调递增的新标识；
  - 删除列只移除该列，其标识永久保留、不回收；
  - 改名**只改名字**，标识不变；
  - 因此「删除一列后再新增同名列」会得到一个**全新标识**，新旧两列不会被混淆。

结构按版本保存：建表为版本 1，每次成功的 `Evolve` 原子生成下一个版本；
每个版本的结构快照一经生成即**不可变**。

## 演进操作

`Evolve([]Change)` 原子地应用一批操作（一批共同形成一个新版本）：

| 操作 | 含义 | 使用字段 |
| --- | --- | --- |
| `ChangeAdd` | 在末尾新增一列 | `Name`、`Default` |
| `ChangeDrop` | 删除指定标识的列 | `ID` |
| `ChangeRename` | 修改指定标识列的名字 | `ID`、`Name` |

一批内按顺序在同一工作副本上校验、应用，因此「新增后随即对其改名」合法。

**非法演进而被整体拒绝**，且拒绝不留下任何痕迹——版本列表与标识分配都不变
（被拒绝批次中即便“预分配”过标识，该标识也不会被消耗）。非法情形包括但不限于：
空批次、未知操作类型、新增空列名 / 重名列、删除或改名不存在（含已删除）的列、
改名为空 / 改为重名、同批重复操作等。

## 解码规则

`Decode(Event)` / `DecodeBatch([]Event)` 把事件解码为当前最新结构的一行：

1. **按列的稳定标识取值，绝不按名字或位置。**
   - 事件的每个值通过「事件所在版本」的 `标识 -> 位置` 索引定位；
   - 这样即使中间发生过删除（位置左移）或改名，旧值仍精确落到同一列。
2. **事件中缺少的列取「当前最新结构」的默认值。**
   - 当一列在事件写入之后才新增，事件所在版本没有该标识，解码结果取当前默认值，
     并在结果中标记来源为 `SourceDefault`。
3. **空串是合法值，不等于缺列。**
   - 事件里的 `""` 照常作为该列的值，来源标记为 `SourceEvent`；
   - 「缺列取默认值」即使默认值恰好也是 `""`，来源仍是 `SourceDefault`，二者可区分。
4. 结果中可用 `Row.Get(id)` 取 `(值, 来源, 是否存在)`，或用 `Row.Ordered()`
   按当前列顺序拿到 `(列定义, 值, 来源)`。

### 可区分的拒绝原因

| 错误码 | 触发条件 |
| --- | --- |
| `ErrInvalidInitialColumns` | 建表列为空、列名为空或重名 |
| `ErrVersionNotFound` | 事件 / 查询引用了不存在的版本号（含 0、负数） |
| `ErrValueCountMismatch` | 事件值个数与所在版本列数不符 |
| `ErrInvalidChange` | 演进操作非法（见上） |
| `ErrTooManyVersions` | 版本数超过上限（`WithMaxVersions`，默认 4096） |
| `ErrInvalidArgument` | 其它入参非法 |

用 `AsErrorCode(err)` 提取错误码做分支处理。

### 批量原子性

`DecodeBatch` 中**任一**事件被拒，则整批失败并返回错误，**不返回任何部分结果**。
整批在同一最新结构快照下解码。

## 并发与确定性

- 演进由互斥锁串行化；解码使用读锁，可被**并发调用，也可与演进并发**。
- 每次解码在锁内同时固定「事件版本快照」与「当前最新快照」，
  因此每个结果都**完整基于某一版本的结构**，不会读到撕裂状态。
- 同一输入反复解码，输出**完全相同**。

## 日志

默认日志输出到 `os.Stderr`，可用 `WithLogger` / `WithLogWriter` 替换。
解码日志会打印**输入**（版本、逐位置的值，空串显示为 `""`）、**解码结果**
（`标识=列名=值(来源)`）与**判定依据**（`by-stable-id` 逐列标注来自事件还是当前默认值）；
被拒绝的操作也会打印输入与可区分的原因。

## 快速示例

```go
r, _ := ontology.NewRegistry([]ontology.ColumnSpec{
    {Name: "a", Default: "DA"},
    {Name: "b", Default: "DB"},
    {Name: "c", Default: "DC"},
})

r.Evolve([]ontology.Change{{Kind: ontology.ChangeDrop, ID: 2}})          // 删除 b
r.Evolve([]ontology.Change{{Kind: ontology.ChangeAdd, Name: "b", Default: "NEW_B"}}) // 新 b = 新标识

row, err := r.Decode(ontology.Event{Version: 1, Values: []string{"1", "OLD_B", "3"}})
// #1 a="1"   from event
// #3 c="3"   from event
// #4 b="NEW_B" from current-default   <- 旧 b(OLD_B) 属于已删除标识，不会灌入新同名列
```

## 本地验证

```bash
# 拉取依赖
go mod tidy

# 全量测试
go test ./...

# 带竞态检测与详细输出（组件支持解码与演进并发，建议必跑）
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestDecode ./ontology
go test -run Example ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 代码检查与格式化
gofmt -l .
go vet ./...
```

### 测试覆盖要点

- 删除后再新增同名列得到**新标识**，旧值不按名字 / 位置错配；
- 空串（来自事件）与缺列（取当前默认值）的来源区分；
- 专门构造「按位置映射会错」「按名字映射会错」的反例，断言按标识才正确；
- 各类非法输入的可区分错误码，拒绝后版本列表与标识分配不变；
- 版本数超限拒绝；批量解码的原子性；同一输入的确定性；
- 解码与演进高并发下的竞态检测（`-race`）；
- 日志包含输入、结果与判定依据。
