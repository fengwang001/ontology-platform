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

## 动态最小生成森林

`DynamicMSF` 在无向带权多重图上增量维护唯一最小生成森林，主实现位于 `mst.go` 与 `forest_paths.go`。

### 全序与唯一性

- 边的大小按 `(weight, id)` 字典序比较：先比较权重；权重相同则编号更小者更小。
- 编号从 1 开始严格递增，被拒绝的 `AddEdge` 不消耗编号。
- 因此任意两条边都可比较，按该全序执行 Kruskal 得到的最小生成森林唯一。
- 允许平行边和负权重；权重范围为 `[-10^9, 10^9]`。

### 更新与报告

- 接受的 `AddEdge`、`SetWeight`、`RemoveEdge` 都使版本号加一；`SetWeight` 设置原权重也会加一。
- 每次更新返回：新版本号、`Entered`、`Left`、更新后总权重、更新后连通分量数。
- `Entered` 和 `Left` 均按边编号升序；删除的森林边一定在 `Left` 中。
- 加边或非森林边降权成更优边时，若它小于两端森林路径上的最大边，则替换该最大边；权重相同时编号顺序决定结果。
- 森林边降权不会改变森林，也不会重置 `TreeSince`；森林边增权或同权重设时，会寻找跨越断开两侧的最小存活边。
- 森林边增权后的候选可以是平行边，也可以是它自己；候选为自己时报告为空且森林不变。
- 删除森林边后会从断开两侧的跨割存活边中选择最小者；没有候选时连通分量数加一。

### 历史与查询

- `InForest(id)` 查询边当前是否在森林中。
- `TreeSince(id)` 返回边最近一次进入森林的版本；当前不在森林或边不存在/已删除时为 0。
- 边持续位于森林中时，仅修改权重不会改变 `TreeSince`。
- `Connected(u,v)` 使用组件标签判断连通性。
- `PathMax(u,v)` 返回森林路径上按 `(weight,id)` 最大的边；权重相同取编号较大者。
- `PathMax` 的可区分错误包括：节点越界 `ErrInvalidArguments`、`u==v` 的 `ErrSameNode`、不连通的 `ErrNotConnected`。
- `AddEdge` 按“参数非法、边数已满”的顺序报错；`SetWeight` 按“参数非法、边不存在”的顺序报错；查询不存在或已删除边返回 `ErrEdgeNotFound`。

### 实现要点

- 全量邻接表保存所有存活边，森林邻接表只保存当前森林边。
- 删除森林边或森林边增权时，从断开两端交替 BFS，先发现较小一侧；随后只枚举小侧邻接边寻找跨割候选。
- 若较小侧节点数为 `s`，用于寻找替换边而扫描的节点数 `scanned <= 2s+2`。
- 森林合并时按小侧组件重标组件标签；所有更新和查询由同一个 `sync.RWMutex` 串行化，查询不会看到替换中途状态。
- 相同更新序列会产生相同版本、报告、总权重和 `TreeSince` 历史。

### 本地验证

```bash
go test ./...
go test -race -v ./...
go vet ./...
gofmt -l .
```

测试包含题目示例、错误优先级、编号/版本不回滚、负权重、平行边替换、同权编号决胜、原权重仍升版本、删除后分量变化、`PathMax` 同权取大编号、`n=100000` 长链端点删除时 `scanned <= 4`，以及 2000 组随机更新序列与整图朴素 Kruskal 的逐项对照。随机测试使用 `-v` 时会记录每次输入、输出和判定依据。
