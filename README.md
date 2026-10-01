# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 层级多选过滤分面计数器

核心实现位于 `ontology` 包（`ontology/facets.go`、`ontology/store.go`、
`ontology/path.go`、`ontology/facets_compute.go`），入口为 `ontology.New()`
返回的并发安全 `*Store`。

### 层级节点

- `Add(docID, attrs)` / `Replace(docID, attrs)` 中，`attrs` 是维度名到值列表的映射。
- 每个值是以 `/` 分隔的 1–4 段路径；段非空，因此不得以 `/` 开头或结尾、
  不得含 `//`。同维度内值字符串不得重复。
- 文档在维度 `d` 上的节点集合是其全部值的全部按段前缀（含值本身）的并集，
  同一节点只算一次。例如值 `a/b/c` 与 `a/d` 的节点集合为
  `{a, a/b, a/b/c, a/d}`。
- 限制：每篇文档至多 16 个维度、每个维度至多 32 个值；`attrs` 允许为空映射。

### 多选过滤语义

- `Facets(selected, topN)` 的 `selected` 是维度名到已选值集合的映射，
  `topN` 取值 1–50。
- 匹配集 `M`：对每个生效且已选集合非空的维度，文档在该维度的节点集合与已选
  集合有交集；同一维度内为“或”，不同维度之间为“与”。
- 选父路径会匹配其后代节点（按段判定），但不匹配仅共享字符串前缀的路径：
  选 `a` 不匹配 `ab`，选 `a/b` 不匹配 `a/bc`。
- 已选集合为空等同于该维度没有约束（而不是匹配不到文档）；已选维度不存在于
  任何文档时没有文档满足该约束，`Total=0`。

### 维度生效规则

- `Link(child, parent)` 声明 `child` 的生效依赖 `parent`；每个 `child` 至多
  一个 parent，声明后不可更改；`Link` 可以先于文档登记，也可以指向不出现在
  任何文档中的维度。
- 维度 `d` 生效当且仅当 `d` 没有 parent，或其 parent 生效且 parent 的已选
  集合非空。
- 不生效维度的已选集合整体被忽略：既不约束匹配，也不出现在结果中。

### 计数与排序规则

- `Total = |M|`。
- 对每个生效维度（维度名字节序）返回候选值列表；
  `count(d,v)` 为满足除 `d` 之外全部生效维度约束（忽略 `d` 自身已选集合）、
  且节点集合含 `v` 的文档数。
- 每个文档对每个节点至多计一次：父节点计数不等于子节点计数之和，一个维度的
  计数之和也可以大于文档数。
- 候选值 = `count > 0` 的节点 ∪ 该维度已选值（已选值计数可以为 0）；
  按 `count` 降序、值字节序升序取前 `topN`，已选值不享受截断豁免；
  每项包含值、`count` 与是否被选。没有候选值的生效维度不出现在结果中。

### 错误（按顺序只报第一个，被拒绝操作不改变任何状态）

- 参数非法：`ErrInvalidArgument`——空 `docID`/维度名、非法路径、同维度值重复、
  维度或值数量超限、`topN` 越界；`Replace` 的参数非法先于文档不存在判定。
- `ErrDuplicateDocument`：`Add` 时 `docID` 已存在。
- `ErrDocumentNotFound`：`Replace`/`Delete` 时 `docID` 不存在。
- `ErrDuplicateLink`：`child` 已声明过 parent。
- `ErrCyclicDependency`：`child == parent` 或新增边使依赖链成环。

### 并发与可复现性

- 增删改、`Link` 与 `Facets` 均可并发调用，内部由读写锁保护，
  结果等价于某个串行顺序；`Replace` 在单个临界区内完成，计数不会同时观察到
  新旧内容。
- `Facets` 先取不可变快照再计数，结果只取决于当前文档集合、依赖关系与
  `selected`，与登记顺序无关；返回值为深拷贝，不别名内部状态。

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

# 对拍（2000 组随机操作序列，含输入/输出/判定依据日志）
go test -v ./ontology -run TestRandomDifferentialAgainstNaive

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
