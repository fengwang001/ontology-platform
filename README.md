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

## 视图依赖 DAG 增量刷新器（`ontology` 包）

按"登记标脏 → 按批拓扑刷新"工作的视图依赖有向无环图：

- **标脏（不立即刷新）**：`SetBase` / `SetBases` 只把基底设值登记到本批待处理表，
  并沿反向边把所有传递依赖视图加入脏集；此时读取仍是上一批已生效值。
- **拓扑重算**：`Refresh` 一次性处理本批累计变更——先让待处理基底全部生效，
  再沿构造时计算好的全局拓扑序重算脏视图，保证任何视图都排在其全部直接依赖之后；
  每个脏视图在一次刷新中恰好重算一次，且先向变更日志写入 `retract`（撤回旧值），
  再写入 `assert`（建立新值）。即使新旧值相等也记录一次。
- **批内去重**：同一批内对同一基底多次设值只保留最后一次（待处理表按名覆盖）；
  共享子图（多个视图依赖同一上游）只通过脏集去重重算一次。
- **视图语义**：视图值恒等于其去重后全部直接依赖的当前值之和；重复依赖边、
  邻接遍历与同层视图顺序均按名称排序，浮点加法顺序固定，因此结果可复现。

### 可区分的整体拒绝原因

构造（`New`）与设值均为事务式失败：一次失败不改变脏集、变更日志与已登记变更。
用 `errors.Is` 区分：

- `ErrEmptyName`：基底名、视图名或依赖名为空
- `ErrUnknownDep`：视图引用了不存在的依赖名
- `ErrUnknownBase`：对未登记的基底设值
- `ErrCycle`：依赖图成环（Kahn 算法后仍有视图入度未归零）
- `ErrTooManyViews`：视图数超过 `maxViews`（0 表示不限）
- `ErrInvalidLimit`：`maxViews` 为负数
- `ErrDuplicateName`：基底与视图重名；`ErrNotFound`：查询了未登记名称

### 并发语义

`Snapshot` / `Value` / `SelfCheck` / `ChangeLog` / `Dirty` 均在读锁下完成，
`Refresh` / 设值使用写锁；快照是深拷贝，同一实例并发读取看到的视图逐字段一致。

### 本地验证方法

用自底向上的全量重算作为"神谕"核对增量结果：

1. 任意设值后（刷新前后均可）调用 `RecomputeAll()`，它忽略脏标记，
   按拓扑序对全部视图用"登记后的基底当前值"自底向上重新求和。
2. `Refresh()` 后调用 `Snapshot().Views`，与全量重算结果逐视图比较，必须完全相等。
3. 调用 `SelfCheck()`：它对每个非脏视图复核"当前值 == 直接依赖当前值之和"。

命令行验证：

```bash
# 详细日志：单测会打印每次设值、脏集、刷新顺序、变更日志与判定依据
go test -v ./ontology

# 并发安全（竞态检测）
go test -race ./ontology

# 覆盖率
go test -cover ./ontology
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
