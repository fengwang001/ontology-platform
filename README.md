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

## 凸多边形区域集合（`regions` 包）

`regions` 包实现带内部禁区（洞）的严格凸多边形区域集合，支持按优先级定位整数坐标点。

### 数据模型

- `Point{X, Y int64}`：整数坐标顶点，范围 `[-1e9, 1e9]`。
- `Ring`：顶点按顺时针或逆时针顺序给出的严格凸多边形环（至少 3 个顶点）。
- `Region{ID, Priority, Outer, Hole}`：`Hole` 可选；存在时同样必须是严格凸多边形，
  且其每个顶点都必须严格位于外环内部（不允许落在外环边上）。

### 边界与禁区归属

- 点属于区域，当且仅当点位于外环的**闭区域**内（外环的边与顶点算内），
  并且**不**位于洞的**开区域**内——洞的边与顶点上的点仍然属于该区域。
- 多个区域的边界可以重合：落在公共边界上的点同时命中这些区域。
- `Locate(x, y)` 返回命中的全部区域，按优先级（`Priority`）降序、
  同优先级按 `ID` 升序排列；`Best` 返回其中第一个，无命中时 `ok=false`。

严格凸的判定分两层，缺一不可：

1. 每个顶点处相邻三边的叉积全部非零且同号（无共线、无折返，朝向统一）；
2. 对每条有向边，其余所有顶点都严格位于内侧半平面（整圈只绕一周）。

第 2 层用于拒绝五角星这类“每个顶点转向同号、但整圈绕两周”的自交顶点序列。
所有叉积使用 `math/big.Int` 精确计算，坐标在 1e9 内不会溢出，判定结果可精确复现。

### 操作与错误原因

| 操作 | 语义 |
| --- | --- |
| `Put` | 新增区域；`id` 已存在则拒绝 |
| `Replace` | 原子替换同 `id` 的已有区域；`id` 不存在则拒绝 |
| `Remove` | 删除区域；`id` 不存在则拒绝 |
| `Locate` / `Best` | 查询命中区域；查询坐标越界同样拒绝 |

可区分的错误原因（哨兵错误，可用 `errors.Is` 判定）：

- `ErrCoordOutOfRange`、`ErrTooFewVertices`、`ErrOuterNotConvex`、
  `ErrHoleNotConvex`、`ErrHoleNotInside`、`ErrIDExists`、`ErrIDNotFound`。

**检查顺序**（对 `Put`/`Replace` 提交的完整区域）：

1. 坐标越界（外环与洞的所有顶点，`Locate` 查询点同理）
2. 顶点数 < 3（外环先、洞后；无洞则跳过洞检查）
3. 外环严格凸性
4. 洞严格凸性
5. 洞的每个顶点严格位于外环内部
6. 最后才是 `Put` 的 id 已存在 / `Replace` 的 id 不存在

任何被拒绝的操作（包括几何校验失败的 `Replace`）都不会改变原有区域。

### 并发语义

`Set` 内部使用读写锁：写操作（`Put`/`Replace`/`Remove`）互斥，
读操作（`Locate`/`Best`）共享。所有操作的结果等价于某个串行顺序；
一次 `Locate` 在同一把读锁下遍历并复制命中的区域，看到的是某一时刻的完整集合快照。
`Replace` 对 map 中单个 key 的赋值在锁内完成，期间不会出现旧区域与新区域
都不在或都在的中间状态。区域存入时做了顶点切片拷贝，调用方后续修改入参不影响集合。
集合与排序均确定，相同操作序列重放得到完全相同的定位结果。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细日志（含输入、输出与判定依据）
go test -race -v ./regions

# 仅看全点朴素对照 / 并发原子性用例
go test -run TestExhaustiveGrid -v ./regions
go test -race -run TestConcurrentReplaceAtomic -v ./regions

gofmt -l .
go vet ./...
```

`TestExhaustiveGrid` 在 0..12 的小网格上用随机严格凸外环与洞（顺/逆时针随机），
对每个整数点与“逐边叉积符号”的朴素实现做全点对照，校验命中集合与排序完全一致。
