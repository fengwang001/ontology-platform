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

## 层级多选分面计数（`ontology.FacetCounter`）

`facets.go` 提供并发安全的 `FacetCounter`，支持文档增删改、维度依赖声明
与带层级取值的多选分面计数。

### 层级节点

- 值是以 `/` 分隔的 1–4 段路径，段非空，不得以 `/` 开头/结尾、不得含 `//`。
- 文档在维度 `d` 上的节点集合是其全部值的全部段前缀的并集（集合去重）。
  例如值 `a/b/c` 与 `a/d` 的节点集合为
  `{a, a/b, a/b/c, a/d}`；`a` 不匹配 `ab`，`a/b` 不匹配 `a/bc`
  （按段匹配，而非字符串前缀）。
- 每个文档至多 16 个维度、每个维度至多 32 个值，同维度值字符串不可重复。

### API 与多选过滤语义

- `Add(docID, attrs)` 登记文档；`Replace(docID, attrs)` 原子替换（持锁单次
  换图，计数不会同时观察到新旧混合内容）；`Delete(docID)` 删除。
- `Link(child, parent)` 声明依赖，每个 child 至多一个 parent 且不可更改。
- `Facets(selected, topN)`（`topN` ∈ [1,50]）返回 `{Total, Facets}`：
  - 维度 `d` 生效当且仅当它没有 parent，或其 parent 生效且 parent 的已选集合
    非空；不生效维度的已选集合整体被忽略（不约束、不出现在结果中）。
  - `Total = |M|`，其中 M 是同时满足每个“生效且已选非空”维度的文档集合；
    同一维度内多值为或（节点集合与已选集合有交集即可），不同维度之间为与；
    已选集合为空等价于没有该约束。
  - 对每个生效维度（按维度名字节序），`count(d,v)` 统计满足**除 d 之外**
    全部生效维度约束、且节点集合含 `v` 的文档数；每个文档对每个节点至多计
    一次（共享祖先只计一次，父节点计数不等于子节点之和，计数之和可大于
    文档数）。
  - 候选值 = `count>0` 的节点 ∪ 该维度已选值（已选值计数可为 0），按
    count 降序、值字节序升序取前 `topN`；已选值不享受豁免；无候选值的生效
    维度不出现。每项含 `Value`、`Count`、`Selected`。

### 可区分的拒绝原因

哨兵错误（用 `errors.Is` 判定），按规定顺序只报第一个，拒绝操作不改状态：

- `ErrInvalidArgument`：空 docID/维度名/Link 参数、非法路径、同维度重复值、
  维度或值超限、`topN` 越界、selected 中非法维度名或值。
- `ErrDuplicateDocument`：`Add` 已存在的 docID。
- `ErrDocumentNotFound`：`Replace`/`Delete` 未知 docID（Replace 参数非法先报
  参数非法）。
- `ErrDuplicateLink`：child 已声明 parent（含重复声明同一条边）。
- `ErrCyclicDependency`：child==parent 或新边使依赖链成环。

### 并发与可复现

所有方法经 `sync.RWMutex` 串行化，结果等价于某个串行顺序；返回结果不别名
内部状态，输入也不被保存引用；计数结果只取决于当前文档集合、依赖关系与
selected，与登记顺序无关，相同操作序列重放输出完全一致。

### 本地验证

```bash
# 全量测试（定向用例 + 2000 组随机操作序列对拍朴素实现 + 并发竞态检测）
go test -race -v ./...

# 仅运行 2000 组对拍（日志含每组输入、got/want 输出与判定依据）
go test -run TestRandomDifferential2000 -v ./

# 代码检查
go vet ./...
gofmt -l .
```
