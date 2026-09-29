# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 调用栈采样热点归因（`hotspot` 包）

`hotspot.Aggregator` 把按根到叶给出的调用栈样本（非空函数名序列 + 正整数权重）
聚合成调用树，并在两个级别统计热点。

### 两级统计定义

- 调用树节点按**从根起的完整路径**区分；同一函数名在不同路径（如递归的两层 `F`）是不同节点。
- 节点自身值（`Self`）：以该节点为叶的样本权重和；节点总值（`Total`）：经过该节点的样本权重和。
- 函数自身值：该函数作为叶的样本权重和；函数总值：**包含**该函数的样本权重和。
- 守恒恒等式（任意已接受样本集合、任意查询快照下均成立）：
  1. 全部函数自身值之和 = 已接受样本总权重；
  2. 每个节点 `Total = Self + 所有子节点 Total 之和`（叶节点子节点和为 0）；
  3. 任一函数总值 ≤ 已接受样本总权重。

### 递归去重规则

同一样本中某函数名出现多次（如 `A,F,G,F,H` 中的 `F`）时，**函数总值只计一次权重**；
但调用树仍按路径分别建节点，因此两个 `F` 节点的总值都包含该样本。
叶节点始终是栈的最后一个元素，只有它增加节点与函数的自身值。

### 非法样本与上限语义

以下样本被**整体拒绝**，不创建任何节点、不改变任何统计，并按原因分别计数
（`Snapshot.RejectReasons`）：

- 空栈（`empty_stack`）；
- 函数名为空字符串（`empty_function_name`）；
- 权重非正（`invalid_weight`）；
- 栈深度超过 `Config.MaxDepth`（`depth_exceeded`，0 表示不限）；
- 提交后节点总数会超过 `Config.MaxNodes`（`node_limit_exceeded`，0 表示不限）。

节点上限在任何写入前预检：样本所需的新节点数（路径上尚不存在的前缀数）
会使节点总数超限即整体拒绝，不留半成品节点；只经过已有路径的样本
（包括在已有中间节点处结束的样本）仍然接受。

### 并发与可复现性

- `Submit` 与 `Query` 均可并发调用；每次提交在单一临界区内完成校验、建点与计数，可线性化。
- 统计只依赖被接受样本集合（权重加法可交换），故并发/乱序提交后的结果与任意顺序串行提交相同。
- `Query` 返回独立拷贝的一致快照；热点列表按总值降序、并列按函数名升序。
- 可用 `hotspot.WithLogger(io.Writer)` 打开判定日志：接受/拒绝、输入栈与权重、
  拒绝原因、去重后的函数集合、快照与排序依据都会打印（`log/slog`）。

本地验证：

```bash
go test -race -v ./hotspot                 # 递归归因、恒等式、非法样本、节点上限、并发
go test -race -count=5 ./hotspot           # 重复运行放大并发交错
go test -run TestDecisionLogging -v ./hotspot
go vet ./... && gofmt -l .
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
