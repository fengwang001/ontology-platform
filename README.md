# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## fusion：多路检索得分归一化融合器

`fusion` 包实现带来源时效与名次惯性的多路检索得分融合，核心类型为 `Fuser`（`NewFuser()` 创建），全部方法可并发调用，结果等价于某个串行顺序。

### 来源与提交

- `AddSource(name, weight, higherBetter, ttl)` 按登记先后加入来源：`weight` 为 1..1000 的整数，`ttl` 为 1..1000000 的整数，`higherBetter` 为真表示原始分数越大越好。
- `Submit(name, hits, now)` 用 `hits` 整体替换该来源当前列表并记下提交时刻；同一列表内同一 doc 只保留对该来源最好的分数（`higherBetter` 取最大，否则取最小）。空列表提交会被拒绝，不能用于清除已提交列表。

### 时效与水位

- 合并器记录至今所有成功的 `Submit` 与 `Fuse` 所用 `now` 的最大值作为水位 `H`（初值 0）；`now < H` 的调用被拒绝（时钟回退），`now == H` 允许。
- 来源在 `Fuse(now, k)` 时有效，当且仅当已成功提交过且 `now − 提交时刻 < ttl`（严格小于；差恰等于 `ttl` 即已过期）。过期或从未提交的来源整体不参与计算，也不影响其他来源的归一化。

### 归一化与计分

设有效来源 `j` 去重后分数最小值为 `lo`、最大值为 `hi`，文档 `d` 的归一化值：

- `hi == lo` 时恒为 1；
- 否则 `higherBetter` 为真时 `n_j(d) = (s−lo)/(hi−lo)`，为假时 `n_j(d) = (hi−s)/(hi−lo)`，均为精确有理数（`big.Rat`）；
- `d` 不在来源 `j` 的列表中时 `n_j(d) = 0`。

总分为 `Σ weight_j · n_j(d)`，对至少出现在一个有效来源列表中的文档计分。返回分数为既约分数文本 `"分子/分母"`，分母为正，整数写成 `"x/1"`，`0` 写成 `"0/1"`。

### 名次惯性

排序键依次为：总分降序；总分相等时，在上一次成功 `Fuse` 的**完整**名次表（不是只截取前 k 个）中出现过的文档排在未出现过的之前，都出现过的按名次升序；都未出现过的按 doc 字节序升序。每次成功的 `Fuse` 用本次完整排序整体替换名次表，被拒绝的 `Fuse` 不更新。同一 `now` 下对同一状态连续融合得到逐字段相同的结果。

### 拒绝原因

拒绝原因可区分（`errors.Is`），被拒绝的操作不改变任何状态，按顺序只报第一个：

- `AddSource`：`ErrInvalidArgument`（名称空 / weight 或 ttl 越界）、`ErrDuplicateSource`（重名）。
- `Submit`：`ErrSourceNotFound`（来源未登记）、`ErrInvalidArgument`（hits 空 / doc 空 / |score| 超 10^15 / now 为负）、`ErrClockRegression`（now 小于水位）。
- `Fuse`：`ErrInvalidArgument`（k<1 / now 为负）、`ErrClockRegression`、`ErrNoFusableSources`（此刻没有任何有效来源）。

### 本地验证

```bash
# 单元测试（含 TTL 边界、水位、名次惯性、大数精确性等）
go test ./fusion

# 竞态检测
go test -race ./fusion

# 与 big.Rat 朴素实现对拍 2000 组随机操作序列，日志含输入/输出/判定依据
go test -v -run TestDifferential ./fusion
```
