# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 图模式匹配（`matcher` 包）

在对象图上匹配子图模式（节点类型 + 边类型 + 属性约束），返回所有同构意义下去重、绑定一致的匹配。

### 模式表示

- `Graph`：对象图。`Object{ID, Type, Attrs}` 为节点，`Edge{Type, FromID, ToID}` 为有向类型边；所有读写由 `sync.RWMutex` 保护，可并发使用；类型索引与邻接集合始终有序，匹配结果与对象/边的插入顺序无关。
- `Pattern`：
  - `Nodes []NodePattern{Var, Type, Constraints}` —— 节点变量、节点类型约束、属性约束（`==`、`!=`、`>`、`<`）。
  - `Edges []EdgePattern{FromVar, Type, ToVar}` —— 有向边类型约束；自环边（`FromVar == ToVar`）表示同一变量绑定的对象必须存在到自身的边。
- `Match`：`map[变量名]对象ID`，一次完整的变量绑定。

### 匹配规则

- 先做类型 + 属性约束预过滤生成候选域，再回溯搜索（`Matcher.MatchAll`）。
- 尽早剪枝：
  - 变量顺序采用 MRV（候选最少优先，度数/字典序打破平局）；
  - 已绑定邻接点时，用入/出邻接集合的有序交集收窄候选；
  - 绑定前逐条校验与已绑定变量之间的边（含自环）。
- 一致性：同一变量的所有节点声明合并为一个类型与一组约束；回溯中一个变量始终绑定同一个对象。
- 去重：绑定是单射的——不同变量不能映射到同一对象（自环模式除外，其两端本就是同一变量），因此每个「变量→对象」元组至多产出一次；产出时再以规范 key（变量首次声明序）做集合去重兜底。

### 拒绝原因（可区分，被拒查询不返回任何部分结果）

`MatchAll` 在搜索前校验模式，命中以下情况返回对应的哨兵错误（可用 `errors.Is` 判定，或用 `matcher.RejectionKind` 取类别字符串）：

- `ErrDegenerateFullScan`（`degenerate_full_scan`）：模式没有节点声明，会退化为无界全搜索。
- `ErrIsomorphicDuplicates`（`isomorphic_duplicates`）：同一条边被重复声明，会使同一同构映射经多条搜索路径重复返回。
- `ErrInconsistentBinding`（`inconsistent_binding`）：同一变量绑定了不同节点类型，或变量/边声明不完整、引用了未声明变量、约束操作符非法。

### 日志

向 `NewMatcher(logger)` 传入 `*log.Logger`（传 `nil` 关闭日志）。日志依次打印：

- `REJECT ... reason=...`：拒绝的模式与具体原因；
- `MATCH-START pattern=...`：模式的节点与边；
- `DOMAIN` / `PRUNE ... basis=...`：候选域与每一次剪枝的判定依据（约束不满足、邻接无边、对象已被其他变量绑定）；
- `BIND ... basis=...` / `MATCH accept=... basis=...`：接受某次绑定/某个匹配的依据；
- `MATCH-DONE matches=N basis=...`：最终匹配数量与结束依据。

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

# 图模式匹配组件（含竞态检测与判定日志）
go test -race -v ./matcher
go test -race -run TestIsomorphicDedup -v ./matcher

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
