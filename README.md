# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## quadtree：可自动分裂的二维点索引

`quadtree` 包实现并发安全的点四叉树，支持插入、按编号删除与矩形范围查询。

### 格子边界与坐标约定

- 坐标为整数；根区域为 `[0, side) x [0, side)` 的左闭右开正方形，`side` 必须为正的 2 的幂。
- 每个格子同为左闭右开：恰落在分裂线上的点归**右侧或上侧**子格（`x >= x+mid` 归东，`y >= y+mid` 归北）。
- 查询矩形 `[x1, x2) x [y1, y2)` 同为左闭右开；宽或高为零时返回空结果（非错误）。

### 分裂与终止条件

- 叶格内点数**超过**容量 `capacity` 时四等分，点按左闭右开规则重新分配到子格。
- 边长为 1 的格不再分裂，允许超容量，并计入溢出格数（`OverflowCells()` 查询）；
  分裂再分配直接产生的超容量边长 1 子格同样计入。
- 删除不触发格子合并；从溢出格删回容量以内时溢出计数相应减少。

### 查询剪枝规则

- 与查询矩形**不相交**的格子不访问。
- **完全包含**于查询的格子整格收集，不做逐点判定。
- 只有**部分相交**的叶格才逐点判定，判定次数计入 `Stats.PointChecks`（同时报告 `Stats.CellsVisited`）。
- 结果按点编号升序返回。

### 并发与确定性

- `Insert`/`Delete` 持写锁，`Query` 持读锁：查询可与插入、删除并发调用，
  每次查询等价于某一时刻点集上的朴素扫描，绝不会看到分裂进行到一半的状态。
- 所有校验（边长非 2 的幂或非正、容量非正、点越界、编号为空或重复、
  删除不存在的编号、查询矩形左端大于右端）在加锁修改前完成，
  被拒绝的操作不改变任何格子或点，错误可用 `errors.Is` 区分。
- 同一操作序列反复执行得到完全相同的格子划分与查询结果。

### 本地验证

```bash
go test ./quadtree/            # 全部用例
go test -race -v ./quadtree/   # 竞态检测 + 打印输入/输出/判定依据日志
go test -run TestRandomFuzzVsNaive ./quadtree/   # 与朴素扫描随机对拍
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
