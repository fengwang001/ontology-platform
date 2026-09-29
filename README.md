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

## 二维点四叉树索引（`ontology` 包）

可自动分裂的二维整数点索引，支持矩形范围查询与按编号删除；查询结果在分裂、
边界与重复坐标下始终与逐点朴素扫描完全一致。

### 格子边界

- 坐标均为整数；根区域为 `[originX, originX+side) × [originY, originY+side)`，
  所有矩形（根、格、查询）一律**左闭右开**。
- 根边长 `side` 必须是**正的 2 的幂**。
- 四等分时以格中线划分：`x < mx` 归左、`x >= mx` 归右；`y < my` 归下、
  `y >= my` 归上——即**恰落在分裂线上的点归右侧或上侧子格**。
- 查询边界同样左闭右开：点 `x == x1` 不算命中，点 `x == x0` 算命中。

### 分裂与终止条件

- 叶子格内点数 `> capacity` 时四等分为四个子格，点按下述归属下沉；
  下沉后对子格重复该处理（同坐标点会引发连续分裂）。
- 边长为 `1` 的格**不再分裂**，允许超过容量；此类格计入 `IndexStats.OverflowCells`。
- 删除**不触发合并**：点被删空的叶子格仍保留，因此同一操作序列始终产生
  完全相同的格子划分。

### 查询剪枝规则

每个格与查询矩形 `[x0,x1)×[y0,y1)` 的关系三选一：

- **完全包含**（格 ⊆ 查询）：整格收录，格内点不做逐点判定
  （计入 `FullyContainedLeaves`）。
- **不相交**：立即剪枝，不访问任何子格或点（计入 `PrunedNodes`）。
- **部分相交**：叶子格才对其中每个点做一次左闭右开判定
  （计入 `PointChecks`）；内部格继续递归四个子格。

`QueryResult.Stats` 报告 `PointChecks / FullyContainedLeaves / BoundTestedNodes /
PrunedNodes`；结果按点编号升序输出。查询矩形宽或高为零直接返回空；
左端大于右端（或下端大于上端）整体拒绝。

### 并发与原子性

- `sync.RWMutex` 保护：写操作（插入/删除及其引发的分裂）持写锁，查询持读锁，
  因此查询看到的永远是某一时刻的完整结构，不可能读到分裂进行到一半的状态。
- 批量插入/删除先整体校验、后落盘：任一点越界、编号为空、编号重复（含批次内
  重复）或删除不存在的编号，整个批次被拒绝，索引不发生任何变化。
- 可区分的错误（`errors.Is` 判定，`IndexError.Kind()` 取类别）：
  `ErrInvalidSize`、`ErrInvalidCapacity`、`ErrPointOutOfRange`、`ErrEmptyID`、
  `ErrDuplicateID`、`ErrDeleteNotFound`、`ErrInvalidQueryRange`。

### 本地验证

```bash
# 全量测试（日志打印输入、输出与逐点判定/剪枝依据）
go test -v ./ontology

# 竞态检测 + 重复执行（并发读写测试）
go test -race -count=3 ./...

# 覆盖率与静态检查
go test -cover ./...
gofmt -l . && go vet ./...
```

测试覆盖：分裂线与查询边界上的点、1000 个相同坐标点（单位格溢出计数、
整格收录零判定）、20 个固定种子的随机操作序列与朴素扫描对拍、均匀分布下
16×16 小面积查询（2 万点平均仅判定约 20 个点，远小于总点数）、
多写多读并发快照一致性、同一操作序列重复执行结果完全一致。
