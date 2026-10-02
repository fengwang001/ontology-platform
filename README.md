# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多快照可见性压实迭代器（`ontology` 包）

`ontology.Compactor` 在带快照持有计数的运行记录集上执行按条带（stripe）的
压实。压实前后，任一存活快照与 `Latest` 视图对任一键的 `Get` 结果逐一相同；
丢弃与折叠的账目由 `Stats` 精确复现。

### 记录、快照与读取

- `Put(k,v)`、`Merge(k,d)`（语义为加 `d`）、`Delete(k)` 各占一个从 1 递增的
  新序号；被拒绝的操作（空键 `ErrEmptyKey`、值越界 `ErrRange`）不改变状态也
  不消耗序号，空键检查先于越界检查。
- `Snapshot()` 返回当前最大序号（尚无写入为 0）并记一次持有；`Release(s)`
  取消一次持有。同一序号可被多次持有，按次数计；存活快照集 S 为被持有序号
  的去重集合。释放未持有序号、`Get` 既非持有序号又非 `Latest` 均报
  `ErrNotHeld`。
- `Get(k,s)` 只看序号不大于 s 的记录，从新到旧扫描：遇 `Put` 返回
  累计 Merge 和加该 Put 值；遇 `Delete` 仅在此前累计过 Merge 时返回累计和；
  走到最旧则在累计过 Merge 或 `deeper` 含 k 时存在，后者再加上 `deeper[k]`。

### 条带划分

条带边界为 B = S ∪ {Latest}（`Latest` 内部以 `math.MaxInt64` 表示）。
记录序号 q 属于 `stripe(q)` = B 中不小于 q 的最小元素；q 恰等于某快照号时
归属该快照的条带。条带定位使用二分查找，非导出计数器
`stripeProbes` 的单次压实增量不超过

```
In × (⌊log2(|S|+1)⌋ + 1)
```

### 条带内折叠规则

各键独立处理；同一条带内记录按序号从新到旧为 e1..ek：

- e1 为 `Put` 或 `Delete`：原样产出 e1，其余 k−1 条计 `Shadowed`。
- e1 为 `Merge`：向下累加连续 Merge，直到条带内第一条非 Merge 记录 b
  或条带耗尽：
  - b 为 `Put` → 产出 `Put(累计和 + b.Val)`；
  - b 为 `Delete` → 产出 `Put(累计和)`；
  - 无 b → 产出 `Merge(累计和)`。
  产出序号取 e1 的序号；被并入的 j 条（含 b）计 `j−1` 条 `Folded`；
  b 之后条带内剩余记录计 `Shadowed`。

### 墓碑清除与 Merge 转换

某键各条带的产出按新到旧排列后执行收尾：

1. 只要最旧一条是 `Delete` 且 `deeper` 不含该键，就删除它（计
   `TombDropped`），并对新的最旧一条重复检查；
2. 循环结束后，若最旧一条是 `Merge` 且 `deeper` 不含该键，把它改为同序号
   的 `Put`（计 `MergeToPut`）。

`deeper` 含键（含基值为 0）时既不清除墓碑也不转换 Merge，因为更深层仍可能
向旧视图提供值。

### Stats 守恒式

```
In = Out + Shadowed + Folded + TombDropped
```

`MergeToPut` 不改变记录数，因此不计入守恒式右侧。`Compact()` 在一把互斥
锁内构造完整产出后一次性替换运行集，是一个原子步骤；之后写入继续追加，
下次 `Compact()` 在上次产出与新记录上再运行。

### 本地验证

```bash
# 全量测试
go test ./ontology/

# 竞态检测（含并发冒烟用例）
go test -race ./ontology/

# 查看 2000 组随机用例的输入/输出/判定日志
go test -run TestRandomAgainstNaive -v ./ontology/
```

随机差分测试（`TestRandomAgainstNaive`）将同一操作序列重放到实现与逐字按
规则写成的朴素模型上，在每步与每次压实后对所有存活快照及 `Latest` 比较
`Get`，并比较压实后的记录集、`Stats` 与二分探测预算；`-v` 日志打印每组的
输入、输出与判定依据。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
