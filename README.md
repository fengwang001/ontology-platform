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

## 多路检索得分归一化融合器（`fusion` 包）

`fusion.Merger` 把多个检索来源在有效期内的原始分数按来源内最小最大值归一化后加权融合，
支持来源时效（TTL）与名次惯性。全部操作并发安全（内部互斥，等价于某个串行顺序）。

### 来源与提交

- `AddSource(name, weight, higherBetter, ttl)` 按登记先后加入来源：
  `weight ∈ [1,1000]` 整数，`ttl ∈ [1,1000000]`，`higherBetter=true` 表示分数越大越好。
- `Submit(name, hits, now)` 用 `hits` **整体替换**该来源当前列表并记录提交时刻；
  `|score| ≤ 10^15`。同一列表内同一 `doc` 重复时只保留对该来源最好的分数
  （`higherBetter` 取最大，否则取最小）。空列表被拒绝，不能清除已提交结果。

### 时效与水位

- 水位 `H`（初值 0）是至今所有**成功** `Submit` / `Fuse` 所用 `now` 的最大值；
  `now < H` 的调用以「时钟回退」被拒（不改变任何状态），`now == H` 允许。
- `Fuse(now, k)` 时来源有效当且仅当：成功提交过，且 `now − submitAt < ttl`
  （差**恰等于 ttl 即过期**）。过期或从未提交的来源整体不参与计算，
  也不影响其他来源的 `lo/hi`。全部无效时返回「无可融合」。

### 归一化公式（精确有理数 `big.Rat`）

对有效来源 j，设去重后分数最小值 `lo`、最大值 `hi`：

- `hi == lo` 时，列表内每个文档 `n_j(d) = 1`；
- 否则 `higherBetter`：`n_j(d) = (s − lo)/(hi − lo)`；
  否则 `n_j(d) = (hi − s)/(hi − lo)`；
- 文档不在来源列表中时 `n_j(d) = 0`（与列表内最差文档同分）。

总分 `score(d) = Σ weight_j · n_j(d)`，只对至少出现在一个有效来源中的文档计分。
分母乘积即使超过 int64 也保持精确；输出为既约分数文本 `"分子/分母"`，
分母恒正，整数写作 `"x/1"`，零写作 `"0/1"`。

### 排序与名次惯性

排序键依次为：

1. 总分降序；
2. 总分相等时，在「上一次成功 Fuse 的**完整**名次表」中出现过的文档优先，
   都出现过则按其在该表中的名次升序；
3. 都未出现过则按 `doc` 字节序升序。

名次表保存的是上一次成功 `Fuse` 的**全部文档完整排序**（与 `k` 无关），
每次成功的 `Fuse` 用本次完整排序整体替换；被拒绝的 `Fuse` 不更新。
`Fuse` 返回前 `k` 项（`k` 大于文档数时返回全部）。因此同一 `now` 下对同一状态
连续融合结果逐字段相同，相同操作序列重放输出完全一致。

### 拒绝原因（按此顺序只报第一个）

- `AddSource`：参数非法（空名 / `weight` 越界 / `ttl` 越界）→ 名称已登记。
- `Submit`：来源未登记 → 参数非法（空 `hits` / 空 `doc` / `|score|` 越界 / `now<0`）
  → 时钟回退。
- `Fuse`：参数非法（`k<1` / `now<0`）→ 时钟回退 → 无可融合。

错误哨兵：`ErrInvalidArgument`、`ErrDuplicateName`、`ErrSourceNotFound`、
`ErrClockRollback`、`ErrNothingToFuse`（用 `errors.Is` 判断）。

### 本地验证

```bash
# 单元测试（TTL 边界、名次惯性各情形、±1e15 与超大分母精确性等）
go test -v ./fusion

# 竞态检测
go test -race ./fusion

# 2000 组随机操作序列对拍（独立 big.Rat 朴素实现；-v 打印输入/输出/判定依据）
go test -run TestRandomDifferential -v ./fusion

go vet ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
