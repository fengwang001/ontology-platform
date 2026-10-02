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

## 增量桥与二边连通块

`IncrementalBridges` 支持只增边的无向多重图，节点编号为 `0..N-1`，接受的边从 1 开始连续编号，每次接受加边使版本号加一；被拒绝的参数校验、容量错误和查询不会分配编号，也不会改变版本或图状态。

### 定义

- 桥：删去后会让其所在连通分量断开的无向边；同一对节点之间的平行边互为备份，因此两条平行边都不是桥。
- 二边连通块：只通过非桥边互相可达的节点集合；孤立节点自成一块，块标签取块内最小节点编号。
- 块权：块内全部非桥边权重之和。桥的权重不属于任何块；桥失效时连同本次新边权重进入合并后的块。

### AddEdge 判定

- `Link`：两端原本不在同一连通分量。新边是桥，块权不变，`NewWeight=0`。
- `Merge`：两端连通但原本属于不同块。桥树路径上的既有桥全部失效，路径上所有块与新边合并，`Merged` 为旧标签升序，`Unbridged` 为失效旧桥升序，`NewLabel` 取最小旧标签，`NewWeight` 为旧块权、失效桥权重和新边权重之和。
- `Inside`：两端原本已在同一块。新边出生即非桥，只增加该块块权；报告本身 `NewWeight=0`，可通过 `BlockWeight` 查询新值。

### 桥树合并

实现维护连通分量并查集、块并查集，以及用 Link-Cut Tree 表示的桥森林。LCT 中每条真实桥使用一个边节点连接两个顶点节点，因此可以在路径暴露后枚举桥并查询最小权重桥。`Merge` 时先枚举路径上的真实桥，再把这些桥从 LCT 切断，并插入权重为空的虚拟非桥边保持节点路径连通；块并查集只支付一次路径收缩代价。`steps` 只累计 `Merge` 走过并合并的块数，满足题目给定的总额外线性上界要求。

### 历史与并发

`EdgeHistory(id)` 返回 `(加入版本, 首次成为非桥版本)`：

- `Link` 新桥出生时第二值为 0，且以后只可能在某次 `Merge` 变成非桥，不会恢复。
- `Inside` 新边出生即非桥，两个版本相等。
- `Merge` 新边出生即非桥，两个版本相等；同次路径上旧桥的首次失效版本也是当前版本。

所有公开方法都由同一把读写锁保护；LCT 查询会改变辅助树偏好路径，因此路径类查询也使用互斥锁，保证并发调用等价于某种串行顺序，且查询看不到半完成合并。

### 本地验证

```bash
# 全量测试；TestRandomOracle2000 会逐次用 Tarjan 朴素重算桥并对照报告
go test -count=1 ./...

# 竞态检测
go test -race -count=1 ./...

# steps 上界：十万节点长链首尾闭环
go test -run TestLongChainClosureStepsBoundAndReport -count=1 .

# steps 上界：十万节点、五十万条随机边
go test -run TestRandom500000StepsBound -count=1 .
```

随机对照测试会在日志中打印种子、每次输入、返回值、Tarjan 重算出的桥集合和判定依据。
