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

## graph：增量桥与二边连通块维护器

`graph` 包（`graph/maintainer.go`）在只增边的无向多重图上在线维护连通分量、
桥与二边连通块，并精确报告每次加边的影响与每条边的版本历史。

### 定义

- **桥**：删去后使其所在连通分量变成两个的边；两条平行边都不是桥。
- **二边连通块**：由全部非桥边连通起来的极大节点集合，孤立节点自成一块；
  块的标签为块内最小节点编号。
- **块权**：块内全部非桥边权重之和；桥的权重不属于任何块，桥变为非桥时
  其权重并入合并后的块；孤立节点块权为 0。
- **桥树**：把每个块缩成一点后得到的森林，恒有
  `BlockCount == BridgeCount + ComponentCount`。

### AddEdge 的三种 Kind

- **Link**：u、v 原不在同一连通分量。新边是桥，块与块权不变。
- **Merge**：u、v 在同一连通分量但不同块。桥树上两块之间路径上的全部桥
  变为非桥，路径上全部块合并为一块（新标签取旧标签最小者），新边为非桥。
  返回 `Merged`（被合并块旧标签升序）、`NewLabel`、`Unbridged`（本次失效的
  既有桥编号升序，不含新边）与 `NewWeight`（旧各块块权 + Unbridged 各边
  权重 + 新边权重）。
- **Inside**：u、v 原已在同一块。无任何结构变化，新边为非桥，其权重并入
  该块块权（`NewWeight` 返回 0）。

### 合并规则与均摊代价

加边用三个并查集维护：块（2ECC）、连通分量、以及桥树上的父指针。
Link 时把较小连通分量的桥树在端点块处换根后挂到另一分量（每个节点的父
指针最多翻转 ceil(log2 N) 次）；Merge 时从两端块交替向上走找到最近公共
祖先，把路径上所有块并入祖先块并做路径压缩。非导出计数器 `steps` 累计
AddEdge 沿桥树走过的节点步数，任意操作序列下满足
`steps ≤ N×(ceil(log2 N)+2) + 4×AddEdge调用数`：每条合并路径只付一次
代价，而不是每次加边重新求全图的桥。

### 版本与历史口径

- 版本号初值 0，每次被接受的加边使版本加一；被拒绝的加边不消耗编号也不
  升版本。
- `EdgeHistory(id)` 返回 `(加入版本, 首次成为非桥的版本)`；出生即非桥两者
  相等，仍是桥则第二项为 0。桥一旦变成非桥便永不恢复。
- 相同操作序列重放得到完全相同的报告与各边历史。

### 错误口径

可区分的原因（`errors.Is` 判定）：`ErrInvalidArgument`（参数/节点非法）、
`ErrEdgeLimit`（边数已满）、`ErrNoSuchEdge`（编号未分配）、
`ErrNotConnected`（两端不连通）、`ErrNoBridge`（路径上无桥）。
AddEdge 按参数非法、边数已满的顺序只报第一个；BridgesOnPath 与 MinBridge
按节点非法、不连通、无桥的顺序。

### 并发

全部方法可并发调用，内部互斥锁保证结果等价于某个串行顺序，查询看不到
只完成一半的合并。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素 DFS 低点值实现对拍、
# 长链闭环与 50 万随机边的 steps 上界、并发与重放确定性）
go test ./graph/

# 详细日志（打印每组随机对拍的输入、输出与判定依据、steps 实测值与上界）
go test ./graph/ -v

# 竞态检测
go test -race ./graph/
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
