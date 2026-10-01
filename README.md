# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 凸多边形区域集合

`ontology.RegionSet` 维护 `(id, priority, outer, hole)` 区域：

- `Outer` 与可选的 `Hole` 都使用整数坐标，坐标绝对值不得超过 `1e9`，且至少有 3 个顶点。
- 外环和洞都必须是严格凸多边形；顶点可按顺时针或逆时针顺序提供，不允许共线、自交或整圈绕多周。
- 洞的每个顶点必须严格位于外环内部，落在外环边上也会拒绝；洞是凸多边形，因此顶点都在内部即可保证洞体位于外环内部。
- 点在外环内部、外环边或外环顶点时算命中；点在洞的严格内部时不命中，但在洞的边或顶点上仍命中。
- `Locate(x, y)` 返回全部命中区域，按优先级降序排列；优先级相同按 `id` 字典序升序。`Best` 返回首个结果。
- `Put` 要求新 `id` 不存在；`Replace` 原子替换已存在的 `id`；`Remove` 删除已存在的 `id`。

新增、替换和查询都先检查坐标范围。区域写入的几何与身份检查顺序固定为：

1. 输入顶点坐标越界（`ErrCoordinateOutOfRange`）
2. 外环或洞顶点数小于 3（`ErrTooFewVertices`）
3. 外环不是严格凸（`ErrOuterNotConvex`）
4. 洞不是严格凸（`ErrHoleNotConvex`）
5. 洞顶点没有严格位于外环内部（`ErrHoleOutsideOuter`）
6. 身份检查：`Put` 的 `id` 已存在（`ErrRegionExists`），或 `Replace`/`Remove` 的 `id` 不存在（`ErrRegionNotFound`）

`Locate` 的查询坐标使用同一个越界错误。任何校验失败都不会修改集合；`Replace` 在校验和身份检查通过后，只在写锁内执行一次 map 赋值，因此不会出现旧版本和新版本同时缺失或同时存在的状态。查询在读锁内扫描同一时刻的完整集合，并复制命中结果；相同操作序列的输入、优先级和排序规则均确定，因此重放得到相同定位结果。

几何判定使用 `int64` 叉积，不使用浮点。本实现的叉积差值最大为 `2e9`，乘积最大为 `4e18`，减法最坏绝对值为 `8e18`，仍在 `int64` 范围内。

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

# 区域集合测试（打印输入、输出和判定依据）
go test -v ./ontology

# 并发原子性与竞态检测
go test -race ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
