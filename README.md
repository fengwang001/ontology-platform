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

## 图模式匹配组件

对象图上的子图模式匹配由两个包提供：

- `graph`：并发安全的对象图（带类型节点 `Object` + 有向带类型边 `Link`）。
- `matcher`：模式定义、回溯匹配、约束检查与同构去重。

### 模式表示

模式由**节点变量**与**有向边模式**组成（见 `matcher/pattern.go`）：

- 节点变量 `NodeVar{Name, Type, Constraints}`：
  - `Name` 是变量名，模式内唯一；同一变量在一次匹配中始终绑定同一个对象。
  - `Type` 是节点类型约束（空串表示不限制类型）。
  - `Constraints` 是该节点属性上的合取约束，算子支持 `==`、`!=`、`>`、`<`。
- 边模式 `EdgePat{Type, Source, Target}`：要求图中存在
  `Source --Type--> Target` 的有向边。

```go
p := &matcher.Pattern{
    Nodes: []matcher.NodeVar{
        {Name: "senior", Type: "Person",
            Constraints: []matcher.Constraint{{Attr: "age", Op: matcher.OpGt, Value: 28}}},
        {Name: "employer", Type: "Company"},
    },
    Edges: []matcher.EdgePat{
        {Type: "worksAt", Source: "senior", Target: "employer"},
    },
}
matches, stats, err := matcher.New(matcher.WithLogger(logger)).
    Match(ctx, g.Snapshot(), p)
```

### 匹配与剪枝规则

- 匹配结果 `[]Match` 是“变量名 → 对象 ID”的绑定；不同变量绑定不同对象（注入嵌入）。
- 搜索采用回溯：
  1. 初始候选按节点类型取对象，并用属性约束立即过滤。
  2. 变量顺序按 MRV（最少候选优先）+ 连通优先确定，尽早失败。
  3. 每次绑定后立即检查与已绑定邻接变量的有向边一致性，并对未绑定邻接变量做前向检查（任一变量候选被清空即剪枝）。
  4. 每个完整嵌入再做一次完整模式校验（类型、属性、所有边），作为最终判定。
- `Stats` 报告候选数、约束剪枝数、边剪枝数、回溯次数、去重前后嵌入数。

### 同构去重规则

- 两个嵌入是**同一个同构匹配**，当且仅当存在一个保持节点类型、约束签名与全部有向类型边的模式自同构 σ，使两个嵌入只相差 σ。
- 匹配器先用节点签名 + 有向类型边做 WL 风格划分，缩小自同构候选类，再在类内枚举置换并用邻接矩阵判定；对每个嵌入取自同构群下的最小规范化键（canonical key）。
- 规范化键相同的嵌入只返回一次，因此对称模式（如两人在同一公司工作）不会因变量互换而重复返回；匹配按变量名排序后的绑定序列输出，顺序确定。
- 模式变量上限为 12，超过将被静态拒绝。

### 查询拒绝（可区分原因，且不返回部分结果）

被拒绝的查询返回错误且 `matches == nil`，用 `errors.Is` 区分：

- `matcher.ErrEmptyPattern`：模式为空，会退化为全图全搜索。
- `matcher.ErrUnconstrainedVariable`：存在无边相连且无类型/属性约束的变量，会退化为笛卡尔积式全搜索。
- `matcher.ErrPatternDefinition`：模式定义非法（空变量名、重复变量、边引用未定义变量、非法算子等）。
- `matcher.ErrFullScanDegeneration`：无类型变量试图枚举全图候选，或嵌入未通过完整模式校验。
- `matcher.ErrInconsistentBinding`：同一变量在一次匹配中被要求绑定不同对象。
- `matcher.ErrDuplicateMatch`：同一组对象映射在去重后仍重复出现。

### 并发与确定性

- `graph.Graph` 内部使用读写锁，写入采用**写时复制**并整体替换不可变快照；`Snapshot()` 返回的视图不受后续写入影响。
- `Matcher.Match` 无共享可变状态，可并发调用；并发匹配同一图得到一致结果。
- 快照中所有邻接表、边列表均排序存储，同一图以**任意边插入顺序**构建都会得到完全相同的匹配集合。

### 日志

注入 `matcher.Logger`（标准库 `*log.Logger` 直接满足）后，每个查询打印：

- `match START`：模式（节点类型、约束、边类型）、各变量候选数、变量顺序；
- `match ACCEPT`：接受的绑定与判定依据（类型 + 约束 + 全部边满足）、规范化键；
- `match DEDUP`：被抑制的同构重复及原因（canonical key 已出现）；
- `match REJECT`：被拒绝的模式与原因；
- `match DONE`：接受数、去重前后嵌入数、各类剪枝数与回溯数。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测（覆盖并发匹配、并发读写）+ 详细日志
go test -race -v ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# 可运行示例（含完整日志输出）
go test -run Example -v ./matcher/
```

关键测试：

- `TestMatch_BasicPattern` / `TestMatch_Constraints`：模式匹配与约束检查；
- `TestMatch_IsomorphicDedup` / `TestCanonicalKey_*`：同构去重与规范化键；
- `TestMatch_SameVariableSameObject`：同变量绑定一致；
- `TestMatch_PruningByEdges` / `TestMatch_EmptyCandidatePrunesImmediately`：剪枝；
- `TestReject_*`：六类拒绝原因及“不返回部分结果”；
- `TestConcurrentMatch_*`：并发一致、边插入顺序无关、无数据竞争。
