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

## 物化视图依赖图（`ontology` 包）

`ontology` 包实现了一个有向无环图（DAG）上的物化视图容器，支持级联失效、
按依赖顺序的拓扑重算与并发一致读取。

### 概念

- **基视图**：`Dependencies` 为空，只能通过 `SetBase` / `SetBaseAndRecompute`
  由外部设值，不能携带计算函数。
- **派生视图**：`Dependencies` 非空，必须提供 `Fn`；其值只能由依赖的当前值推导，
  不接受直接写入。
- **物化**：视图只有在完成过一轮成功重算后才有值；此前读取返回
  `not_materialized`。

### 失效传播规则

1. 对基视图设值时，该视图连同其**全部下游传递闭包**（直接/间接依赖它的视图）
   被标记为脏。
2. 脏标记是集合语义：同一轮内对同一视图重复失效只保留一个脏位，因此每轮
   每个视图**至多求值一次**。
3. 重算只处理脏视图；未标记为脏的视图**不会被求值**（其旧值继续可读）。

### 拓扑序与重算

- 重算在脏子图上做 Kahn 拓扑排序，保证**依赖先于被依赖者**求值；
  并列节点按**注册顺序**打破平局，使求值顺序与结果可复现。
- 一轮重算先把所有新值计算到临时表中，全部成功后才一次性提交；
  任何错误（依赖未注册、基视图未设值、`Fn` 返回错误）都会丢弃临时结果，
  **图状态与轮次号保持不变**，随后可用合法输入重试。
- 注册允许**前向引用**（依赖可以稍后注册）；注册时做环检测，成环（含自环）
  整体拒绝。重算时若依赖仍未注册，返回 `unknown_dependency`。

### 并发一致性

- 内部使用写优先读写锁：`SetBaseAndRecompute` 全程持写锁，设值与重算
  对读者是一个不可分割的操作。并发 `Get` / `Snapshot` / `Verify`
  只能观察到**某一轮完整重算之前或之后**的状态，绝不会读到半轮混合值。
- `Snapshot` 返回值的副本，可在锁外安全使用；`Generation` 单调递增，
  标识当前已完成的重算轮次。

### 错误种类（可区分的拒绝原因）

| ErrorKind | 触发场景 |
| --- | --- |
| `empty_name` | 注册空名称 |
| `duplicate_name` | 注册重名视图 |
| `cycle_detected` | 依赖成环或自环 |
| `unknown_dependency` | 重算时依赖尚未注册 |
| `name_not_registered` | 读/写未注册名称 |
| `not_base_view` | 对派生视图设值 |
| `base_view_with_fn` | 基视图携带计算函数 |
| `derived_without_fn` | 派生视图缺少计算函数 |
| `not_materialized` | 读取从未成功重算过的视图 |
| `base_not_set` | 重算闭包内基视图尚未设值 |
| `compute_failed` | 用户计算函数返回错误或全量对照不一致 |

### 使用示例

```go
g := ontology.NewGraph()
g.Register(ontology.View{Name: "a"})
g.Register(ontology.View{Name: "b", Dependencies: []string{"a"},
    Fn: func(d []any) (any, error) { return d[0].(int) * 2, nil }})

if err := g.SetBaseAndRecompute("a", 21); err != nil {
    log.Fatal(err)
}
v, _ := g.Get("b") // 42
snap, _ := g.Snapshot() // map[a:21 b:42]
```

### 本地验证方法

包内提供与增量重算相互独立的**全量重算对照**自检 `Verify()`：
它从所有已设值的基视图出发，按拓扑序重新推导每个派生视图，
再与当前物化值用 `reflect.DeepEqual` 逐一核对。

```bash
# 全量测试（含 8 读者并发 × 200 轮原子设值重算的半轮混合检测）
go test -race -v ./ontology

# 查看注册顺序、设值/重算后各视图值与判定依据的日志
go test -v ./ontology 2>&1 | grep -E "注册|物化值|判定依据"

# 覆盖率
go test -coverprofile=coverage.out ./ontology
go tool cover -html=coverage.out
```
