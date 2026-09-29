# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 视图依赖增量刷新器（`ontology` 包）

`ontology.Refresher` 是一个按批拓扑增量刷新器：基底（base）保存原始数值，
视图（view）的值定义为其全部直接依赖当前值之和，依赖关系构成有向无环图（DAG）。

### 标脏规则

- `SetBase` 只登记变更并把该基底的所有下游（传递）视图标脏，**不立刻刷新**；
  刷新前查询基底仍返回旧值。
- 重定义视图依赖（`AddView` 同名）也会把该视图及其下游标脏。
- 脏集可通过 `DirtySet()` 查询，刷新成功后清空。

### 拓扑重算规则

- `Refresh()` 把本批累计的变更一次处理完：先应用全部基底新值，
  再对脏视图做 Kahn 拓扑排序（只统计脏视图之间的边，并列时按登记顺序，
  保证结果可复现），每个脏视图**恰好重算一次**，
  且任何视图都排在其全部依赖之后。
- 重算每个视图时先输出一条 `RETRACT`（撤回旧值）再输出一条 `ASSERT`（建立新值），
  即单次刷新中每个脏视图在变更日志里恰好出现一对撤回/建立记录。
- 空批（无已登记变更）刷新为空操作。

### 批内去重规则

- 同一批内对同一基底多次 `SetBase`，只有**最后一次**生效；
  无论设值多少次，受影响的脏视图在下一次刷新中都只重算一次。

### 失败原子性

空名、未知依赖名、未知基底名、依赖成环、视图数超限分别对应
`ErrEmptyName` / `ErrUnknownDependency` / `ErrUnknownBase` /
`ErrDependencyCycle` / `ErrTooManyViews`（可用 `errors.Is` 判定）。
所有校验通过后才修改状态，一次失败不改变脏集、变更日志与已登记变更。

### 并发

查询（`Get` / `Snapshot` / `DirtySet` / `Changes`）与自检（`SelfCheck`）
均持有读锁，可与刷新安全并发；静止状态下并发读取的视图逐字段相同。

### 本地验证：自底向上全量重算核对

`SelfCheck()` 从基底当前值出发，按拓扑序自底向上**全量重算**所有视图，
并与增量刷新后的当前值逐一比对，任一视图不一致即报错。
手动核对方法：

```bash
# 跑全量测试（含竞态检测），日志会打印设值、脏集、刷新顺序、变更日志与判定依据
go test -race -v ./ontology/
```

亦可在代码中于任意次 `Refresh()` 后调用 `SelfCheck()`，
用全量重算结果交叉验证增量结果。

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
