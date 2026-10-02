# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 宽相碰撞对维护器

根包 `broadphase` 提供带膨胀包围盒的二维整数矩形宽相维护器。

### 盒与过滤

- `New(M, C)` 中 `M` 是膨胀边距，`C` 是对象数上限。
- 紧盒为半开区间 `[LX,HX)×[LY,HY)`，要求 `LX<HX`、`LY<HY`。
- 插入时膨胀盒为紧盒四边各扩 `M`；移动时只有新紧盒不完全位于当前膨胀盒内才重取。
- 重取直接使用新紧盒重新扩张，不与旧膨胀盒取并集；膨胀盒不会因落在旧范围内的移动而缩小。
- 两个盒相交要求两个轴都满足一方 `lo < 另一方 hi`；仅在边或点相接不算相交。
- 可碰撞要求双向过滤同时成立：`a.layer & b.mask != 0` 且 `b.layer & a.mask != 0`。

### 操作与事件

- `Insert(id, tight[, layer, mask])` 创建膨胀盒并返回新产生的宽相对与接触对，过滤位缺省为 `(1,1)`。
- `Move(id, tight)` 更新紧盒；`MoveResult.Refatted` 报告是否重取膨胀盒，事件以四个升序列表返回。
- `Remove(id)` 删除对象，并让包含该对象的全部宽相对与接触对退出。
- `SetFilter(id, layer, mask)` 只改过滤位；盒不变，但两类对都可能进入或退出。
- 接触对始终是宽相对的子集；宽相对退出时，若它原本也是接触对，也会同时进入 `ContactExit`。
- `Pairs()` 与 `Contacts()` 返回当前两类对；`FatBox(id)`、`Tight(id)` 返回当前盒。
- 参数非法、对象已存在、对象不存在、容量已满分别返回可区分的哨兵错误；被拒绝操作不改变状态与计数。

### 扫描顺序与计数

膨胀盒的 x 端点按 `(坐标, 类型, 编号)` 排序。坐标相同时 `hi` 排在 `lo` 前，编号小的在前。重取移动时：

- 非导出计数 `crossed` 等于该对象两个 x 端点与其他对象端点的相对次序翻转总数。
- `pairChecks` 只在翻转双方是 `lo` 对 `hi` 且两个对象通过双向过滤时增加，因此不超过 `crossed`。
- 不重取时两个计数均为 0；接触对只按该对象当前宽相对邻接集合重算。
- 插入和删除不增加 `crossed`。

实现使用 x/y 端点 treap 精确定位轴状态翻转，并用一个 x 区间树避免插入时遍历全部对象；所有方法由 `sync.RWMutex` 串行化变更，查询不会观察到半完成重取。

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

宽相维护器专项验证：

```bash
# 题面示例与边界条件
go test -v -run 'TestSpecExample|TestEdge|TestMargin|TestNonCollidable|TestHundredThousand|TestConcurrent'

# 2000 组随机操作与朴素两两比较实现对照；-v 会打印输入、输出集合与判定日志
go test -v -run TestRandomAgainstNaive

# 竞态检测
go test -race ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
