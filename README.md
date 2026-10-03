# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 宽相碰撞对维护器（broadphase 包）

`broadphase` 在二维整数矩形对象上维护两类对：**宽相对**（可碰撞且膨胀盒相交）与**接触对**（可碰撞且紧盒相交，必是宽相对的子集），并对每次被接受的操作返回 `FatEnter` / `FatExit` / `ContactEnter` / `ContactExit` 四个按 `(a,b)` 升序的对列表（`a < b`）。

### 膨胀盒的取法与重取条件

- 插入时膨胀盒 = 紧盒四边各扩 `M`。
- `Move` 时若新紧盒在两轴上整体落在当前膨胀盒内（`lo >= 膨胀盒 lo` 且 `hi <= 膨胀盒 hi`），膨胀盒保持不变，只更新紧盒；否则按新紧盒四边各扩 `M` 重取，**不与旧膨胀盒取并集**（旧范围直接丢弃）。
- `M = 0` 时紧盒缩小到旧膨胀盒之内不会触发重取，膨胀盒不会缩小。
- 任何时刻每个对象的紧盒都落在其膨胀盒之内。

### 相交判定与过滤

- 两盒在某轴上相交当且仅当一方的 `lo` 严格小于另一方的 `hi`；两轴都相交才算盒相交。仅在边上相接（`lo == 对方 hi`）不算相交，紧盒边接也不算接触。
- 对象可碰撞当且仅当 `(a.layer & b.mask) != 0` 且 `(b.layer & a.mask) != 0`（双向都成立）；`layer` 或 `mask` 为 0 的对象不与任何对象碰撞。不可碰撞的对象对即使盒相交也不是任何一类对。

### 端点排序与计数器

- 所有膨胀盒的 x 端点按 `(坐标, 类型, 编号)` 排序：坐标相同时 `hi` 排在 `lo` 之前，再按对象编号升序。
- `Move` 重取膨胀盒时，`Crossed()` 累计该对象两个 x 端点与其他对象端点相对次序发生翻转的总对数；仅当一个 `lo` 端点与另一可碰撞对象的 `hi` 端点翻转时才执行一次 y 轴相交判定并计入 `PairChecks()`，因此 `pairChecks <= crossed`。
- `Insert` / `Remove` / `SetFilter` 与未重取的 `Move` 不计 `crossed` 与 `pairChecks`；未重取的 `Move` 重算接触对时只检查该对象当前的宽相对，不对全部对象两两比较。

### 事件口径

- 退出的宽相对若原是接触对，同时出现在 `ContactExit`；`Remove` 使该对象的全部宽相对与接触对退出；`Move` 额外返回是否重取（`Refit`）；`SetFilter` 不改任何盒与计数器，但可使对进入或退出。
- 参数非法、对象已存在/不存在、对象数已满按此顺序只报第一个原因（`ErrInvalidParam` / `ErrAlreadyExists` / `ErrNotExists` / `ErrFull`，可用 `errors.Is` 区分）；被拒绝的操作不改变任何状态。
- 所有操作与查询可并发调用，效果等价于某个串行顺序（内部读写锁）；相同操作序列重放得到完全相同的事件序列与最终两类对。

### 本地验证

```bash
# 全部测试（含规格示例走查、边界用例、n=100000 计数器上界、
# 2000 组随机操作序列与朴素两两比较实现逐步对照）
go test ./broadphase/

# 竞态检测
go test -race ./broadphase/

# 查看随机对照的输入/输出/判定依据日志
go test -v -run TestRandomAgainstNaive ./broadphase/
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
