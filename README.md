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

## Link 子系统

关系（Link）子系统全部在根包 `ontology` 中，仅使用标准库，状态保存在进程内存，
可选地通过 `SaveToFile` / `LoadFromFile` 以 JSON 快照持久化到本地文件。

### 声明 LinkType

```go
s := ontology.NewStore()
s.RegisterObjectType("Person")
s.RegisterObjectType("Company")
s.RegisterLinkType(ontology.LinkType{
    Name:           "employs",        // 唯一名字
    Source:         "Company",        // 源 ObjectType（可与 Target 相同，支持自引用）
    Target:         "Person",         // 目标 ObjectType
    Cardinality:    ontology.OneToMany,
    SourceRequired: false,            // 源侧必选性（提交时校验）
    TargetRequired: true,             // 目标侧必选性
    OnDelete:       ontology.SetNull, // CASCADE / SET_NULL / RESTRICT
})
```

### 五类违约的即时检测

`CreateLink` 立即校验，五类错误可通过 `IsViolation(err, kind)` 分别判定，
错误（`*LinkError`）中携带 `LinkType`、`Source`、`Target` 与违约类别：

| 类别 | 常量 | 触发条件 |
| --- | --- | --- |
| ONE_TO_ONE 违约 | `ViolationOneToOne` | 源已有链，或目标已被其他源占用 |
| ONE_TO_MANY 违约 | `ViolationOneToMany` | 目标已归属其他源 |
| 端点类型不符 | `ViolationEndpointType` | 端点 ObjectType 与声明不符 |
| 端点不存在 | `ViolationEndpointNotFound` | 源或目标对象不存在 |
| 重复建链 | `ViolationDuplicateLink` | 同一条链已存在 |

必选性（`SourceRequired` / `TargetRequired`）**不**在建链时校验，只在提交时调用
`ValidateRequired()` 校验；它一次性返回全部缺失的必选关系（`*RequiredError.Missing`，
每条含对象 ID、LinkType 与 source/target 侧），而不是遇到第一个就返回。

### 三种级联语义的结算顺序

`DeleteObject(id)` 先**规划**后**执行**，规划阶段沿链递归遍历（visited 集合保证环上
终止且每个对象只结算一次）：

1. `CASCADE`：把对端对象纳入删除集，递归结算它自身的链；
2. `SET_NULL`：把该链纳入断链集，对端对象保留；
3. `RESTRICT`：立即中止规划，返回 `*RestrictError`，其中 `Path` 是从被删对象到该
   RESTRICT 链的完整路径（`[]PathStep`，含每一跳的对象与 LinkType）。

规划成功才一次性应用（先断链、再删对象并清理其残余链）；规划期触发 RESTRICT 时
没有任何状态被修改——已规划连带删除的对象与断链全部天然回滚。

### 批量原子变更

`Batch` 支持混合 `CreateLink` / `DeleteLink` / `DeleteObject`，`ApplyBatch` 在私有
副本上顺序执行，全部成功才换入，失败返回 `*BatchError`（含第几条操作 `Index`、
`Op` 中的 LinkType 与端点，可用 `errors.As` / `IsViolation` 解包）。批内语义确定：

- 批内重复建同一条链：幂等 no-op（不报重复错误）；
- 先建后删同一条链：最终不存在（先删后建则最终存在）；
- 批内建立的链会被同批后续的级联删除正常波及。

### 环检测与索引不变量自检

- `FindCycles(start, linkTypes)`：从起点沿给定 LinkType（双向可遍历）枚举可达的
  基本环，结果去重、按字典序排序，与 map 遍历顺序无关；自环返回单元素环。
- `CheckInvariant()`：校验正反索引互为镜像，不一致时返回 `*InvariantError`，
  指出 LinkType、哪一侧（forward/reverse）以及哪一对端点。任何时候都可调用
  （读锁），适合在并发测试中持续断言。

### 并发安全

所有读写都在单把 `sync.RWMutex` 下进行：建链的“检查+写入”是原子的（两个 goroutine
同时给一个 ONE_TO_ONE 源建链时恰好一个成功），任何时刻外部观察到的正反索引都互为
镜像。可用 `go test -race ./...` 验证。
