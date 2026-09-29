# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多表连接顺序选择

`ontology.ChoosePlan(query Query) (Plan, error)` 根据输入表基数和连接谓词选择确定性的最小代价计划。

### 输入约束

- 表数量必须在 1 到 12 之间；表名非空且不得重复。
- 每个谓词必须引用两个不同的已知表。
- 选择率以正整数 `Numerator/Denominator` 表示，必须满足 `0 < Numerator/Denominator <= 1`。
- 表行数为非负整数；任何输入非法都会整体拒绝并返回可区分的哨兵错误，不返回部分计划。

### 行数与代价

单表输出行数就是输入行数。两个子计划 `L`、`R` 连接时：

```text
rows(L join R) = rows(L) * rows(R) * Π selectivity(p)
```

其中乘积包含所有一端在 `L`、另一端在 `R` 的谓词；同一对表上的多个谓词选择率相乘。所有行计数、选择率和代价均使用 `math/big.Rat` 做精确有理数计算。

计划代价是每次连接输出行数之和：

```text
cost(L join R) = cost(L) + cost(R) + rows(L join R)
```

单表扫描代价为 0。

### 笛卡尔积与并列规则

- 查询图连通时，任何一步的两侧诱导子图都必须连通，因此不会在中间步骤提前做笛卡尔积。
- 查询图不连通时，先分别完成每个连通分量内部的连接，再把完整分量作为整体做笛卡尔积；分量合并顺序也使用同一动态规划选择最小代价。
- 计划文本以表名为叶节点；每次连接写成 `(left right)`，两部分按规范文本的字典序排列，较小者在左。
- 总代价相等时，选择规范文本字典序最小的计划。输入表和谓词的原始顺序不影响结果；函数无可变全局状态，可并发调用。
- `Plan.Partitions` 报告实际考察的合法二分次数；十二表时不超过 `3^12 = 531441`。

### 返回错误

可通过 `errors.Is` 区分：

- `ErrEmptyQuery`
- `ErrTooManyTables`
- `ErrEmptyTableName`
- `ErrDuplicateTableName`
- `ErrUnknownPredicate`
- `ErrSelfPredicate`
- `ErrInvalidSelectivity`
- `ErrNegativeRowCount`

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

# 连接顺序选择器对拍、非法输入、并列与十二表上界
go test -race -v -run 'TestMatchesNaiveExhaustivePlansUpToSixTables|TestTieBreaksByCanonicalText|TestDisconnectedComponentsJoinInternallyThenCartesianMerge|TestPartitionCountBoundedByThreeToTableCount|TestInvalidInputReasons' ./...

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

测试日志会通过 `t.Logf` 打印每个关键用例的输入、输出计划/错误以及判定依据。六表以内随机查询还会枚举全部合法完整二叉连接树，与动态规划结果对拍。
