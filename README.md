# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多表连接顺序选择器

`joinorder` 包根据输入表行数和连接谓词选择确定性的最小代价二叉连接计划。

### 输入与结果

- `Table{Name, Rows}`：表名与非负行数。
- `Predicate{LeftTable, RightTable, Numerator, Denominator}`：选择率 `Numerator / Denominator`，要求正整数且 `0 < Numerator <= Denominator`。
- `Result.Plan.Text`：规范计划文本；叶子为表名，连接写作 `(left right)`。
- `Result.Plan.Rows` 与 `Result.Plan.Cost`：均为 `*big.Rat`，全程精确有理数计算。
- `Result.PartitionsExamined`：动态规划考察的非平凡无序划分数量，不超过 `3^n`，其中 `n` 为表数。

同一对表上的多个谓词选择率相乘。对子计划 `L`、`R`，令 `P(L,R)` 为所有跨两侧谓词选择率的乘积：

```text
rows(L join R) = rows(L) * rows(R) * P(L,R)
cost(L join R) = cost(L) + cost(R) + rows(L join R)
```

单表计划的代价为 `0`。最终代价是整棵树中每次连接输出行数之和。

### 笛卡尔积条件

- 先求查询图的连通分量。
- 查询图连通时，分量内每一步都要求两侧之间至少有一条连接谓词，因此不会产生笛卡尔积。
- 查询图不连通时，每个分量先在内部完成连接，再把分量结果用笛卡尔积合并；分量合并也执行同样的子集动态规划并取代价最小的顺序。

### 确定性规则

- 输入表和谓词在内部按规范键排序，调用方以任意顺序给出数据都得到逐字节相同的计划文本。
- 每次连接的两个子计划按规范文本排序，字典序较小者在左。
- 代价相等时，选择规范文本字典序最小的计划。
- `Selector` 不保存每次调用的可变状态，可被多个 goroutine 并发调用。

### 拒绝原因

非法输入会返回 `ValidationError`，通过 `Reason` 区分：

- `empty_table_set`：表数为零。
- `too_many_tables`：表数超过十二。
- `empty_table_name`：存在空表名。
- `duplicate_table_name`：表名重复。
- `unknown_table`：谓词引用未知表。
- `same_table_predicate`：谓词两端是同一张表。
- `invalid_selectivity`：选择率分子分母非正，或选择率大于一。
- `negative_row_count`：表行数为负。

校验失败时直接返回错误，不返回部分计划。

### 使用示例

```go
selector := joinorder.NewSelector()
result, err := selector.SelectPlan(
    []joinorder.Table{
        {Name: "orders", Rows: 1000},
        {Name: "customers", Rows: 100},
    },
    []joinorder.Predicate{
        {LeftTable: "orders", RightTable: "customers", Numerator: 1, Denominator: 100},
    },
)
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

# 只验证连接顺序选择器（-v 会打印对拍输入、输出和判定依据）
GOCACHE=/tmp/go-cache go test -race -v ./joinorder

# 单个包 / 单个用例
go test ./joinorder
go test -run TestRandomInputsMatchNaive ./joinorder

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
