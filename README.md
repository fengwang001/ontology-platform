# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## compactor：带多快照可见性的压实迭代器

`compactor` 包维护一个按序号递增的运行集（记录为 `键、序号、种类、值`，
种类为 `Put`/`Merge`/`Delete`），支持快照持有与释放、按序号读取 `Get`，
以及一次性重写运行集的 `Compact`。`New(deeper)` 的 `deeper` 表示更深层
已存在的键及其基值（复制保存、此后不变，基值可为 0，存在即算有）。

### 条带划分

存活快照集 `S` 为被持有序号的去重集合（同一序号多次持有只算一个条带边界，
按次数释放）。序号 `q` 的条带 `stripe(q)` 定义为 `S∪{Latest}` 中不小于 `q`
的最小元素（`Latest = math.MaxUint64`；`q` 恰等于某快照号则属于该快照的
条带）。条带定位用二分查找，探测次数不超过
`In × (⌊log2(|S|+1)⌋+1)`（由非导出计数器 `stripeProbes` 统计）。

### 折叠规则

`Compact` 各键独立处理。同一键同一条带内按序号从新到旧的记录 `e1..ek`
产出一条，产出序号取 `e1` 的序号：

- `e1` 为 `Put` 或 `Delete`：原样产出，其余 `k−1` 条计入 `Shadowed`。
- `e1` 为 `Merge`：向下累加连续的 `Merge`，直到条带内第一条非 `Merge`
  记录 `b` 或条带耗尽：
  - `b` 为 `Put`：产出 `Put(累计和+b 的值)`；
  - `b` 为 `Delete`：产出 `Put(累计和)`；
  - 无 `b`：产出 `Merge(累计和)`；
  - 被并入的 `j` 条（含 `b`）计 `j−1` 条 `Folded`，`b` 之后条带内剩余的
    计入 `Shadowed`。

### 墓碑清除与 Merge 转 Put

各条带产出得到后，对该键从新到旧的产出序列做收尾：

1. 只要最旧的一条是 `Delete` 且 `deeper` 不含该键，就删除它
   （计 `TombDropped`），并重复检查新的最旧一条；
2. 循环结束后若最旧一条是 `Merge` 且 `deeper` 不含该键，改为同序号的
   `Put`（计 `MergeToPut`，不改变记录数）。

### Stats 守恒式

`Compact` 返回 `Stats{In, Out, Shadowed, Folded, TombDropped, MergeToPut}`，
账目可精确复现并满足：

```
In = Out + Shadowed + Folded + TombDropped
```

压实前后任一存活快照与 `Latest` 视图的 `Get` 结果逐一相同；相同操作序列
重放得到完全相同的序号、产出与 `Stats`。全部方法可并发调用，结果等价于
某个串行顺序，`Compact` 是一个原子步骤。

### 本地验证

```bash
go test ./compactor/                 # 全部单测 + 2000 组随机序列对照朴素模型
go test -race -v ./compactor/        # 竞态检测 + 打印输入/输出/判定依据日志
go test -run TestExampleOne -v ./compactor/  # 规格示例一
```

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
