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

## 调用栈采样热点归因（`hotspot` 包）

`hotspot.Aggregator` 把按根到叶给出的调用栈样本（非空函数名序列 + 正整数权重）
聚合成调用树，并在节点、函数两个层级统计自身值（Self）与总值（Total）。

### 节点级统计（调用树）

- 节点按“从根起的完整路径”区分：同名函数出现在不同路径上是不同节点
  （递归样本 `A,F,G,F,H` 中的两个 `F` 各自独立）。
- 节点自身值：以该节点为叶的样本权重之和。
- 节点总值：经过该节点的样本权重之和。
- 恒等式：对任意节点，`节点总值 == 节点自身值 + 所有直接子节点总值之和`。

### 函数级统计

- 函数自身值：该函数作为叶出现的样本权重之和。
- 函数总值：包含该函数的样本权重之和；**同一样本中同一函数出现多次（递归）
  只计一次**，因此递归不会把总值放大。
- 恒等式：`所有函数自身值之和 == 已接受样本总权重`；
  `任一函数总值 <= 总权重`。
- 热点列表排序：总值降序，并列按函数名升序。

### 非法样本与上限语义

样本被整体拒绝（任何统计都不改变）并按原因分别计数，归类优先级固定为：

1. `empty_stack`：栈为空（nil 或长度为 0）；
2. `empty_function_name`：栈中存在空函数名；
3. `invalid_weight`：权重 <= 0；
4. `depth_exceeded`：深度超过 `Config.MaxDepth`；
5. `node_limit_exceeded`：节点总数已达 `Config.MaxNodes` 且本样本需要新建节点。

节点上限采用“先离线构造路径、再整体挂树”的两阶段提交：达到上限后，
需要新建节点的样本被整体拒绝且不留下任何残节点；只经过已有路径的样本
（含重复出现的函数）仍然接受。`MaxDepth`/`MaxNodes` 传非正值时使用默认值
（1000 / 100000）。

### 并发与一致性

`Submit` 与 `Query` 可被并发调用：提交在单个互斥区内原子完成，
因此并发乱序提交的结果与任意顺序的串行提交完全相同（统计只依赖样本多重集）；
每次 `Query` 返回排序确定、满足上述全部恒等式的不可变快照。
通过 `WithLogger` 注入日志器后，每次提交/查询都会打印输入、输出与判定依据。

### 本地验证

```bash
# 全量测试（含竞态检测与详细日志）
go test -race -v ./hotspot

# 全仓库
go test -race ./...

# 仅跑递归归因 / 上限 / 并发用例
go test -v -run 'TestRecursiveAttribution|TestNodeLimit|TestConcurrentSubmission' ./hotspot
```

测试覆盖：递归样本 `A,F,G,F,H` 中 F 的归因、三条守恒恒等式、五类非法样本、
节点上限无残留语义，以及 16 个 goroutine 乱序并发提交与串行结果的一致性。
