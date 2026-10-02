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

## Dremel 嵌套记录列式拆分（`ontology` 包）

`ontology` 包实现 Dremel 风格的嵌套记录拆分 / 重装：

- `New(schema []Field, pageEntries, maxEntries int) (*Shredder, error)`：校验模式与参数。
  模式至少 1 个叶子、至多 16 个叶子、嵌套深度至多 6，同级字段名非空且唯一；
  `pageEntries`、`maxEntries` 必须 ≥ 1，否则返回 `ErrSchema` / `ErrParam`。
- `Shred(rec map[string]any) error`：按模式把一条记录拆到各叶子列，整步原子提交；
  被拒绝的记录不会改变任何列、分页与记录计数。
- `Entries/Pages/Stats/Columns/RecordCount`：读取列条目、分页、统计与记录数。
- `Assemble() []map[string]any`：按记录序号从全部叶子列还原规范化记录；
  `EntriesRead()` 返回历次 Assemble 读取的条目总数（内部 `entriesRead` 计数器）。

### 两个级别

对叶子列（点号路径，如 `author.ids`）：

- 重复级 `rep`（maxRep = 路径上 Repeated 节点数）：该条目由第几层
  Repeated 节点“新元素”产生。一条记录在该列的首条目 `rep=0`；
  第 k 个 Repeated 节点（从根起第 1、2、… 个）的第 j≥2 个元素之下的
  首条目 `rep=k`，其余首条目继承外层传入的 rep。
- 定义级 `def`（maxDef = 路径上非 Required 节点数）：路径上已确认存在的
  非 Required 节点数。`def < maxDef` 的条目为空条目（`Entry.Null=true`），
  否则携带 int64 叶子值。

### 条目生成规则

沿叶子路径自根向下：

- Optional 缺键或 nil：写一个空条目（`rep`=继承值，`def`=当前值）后结束该列。
- Optional 存在：`def` 加 1 后继续。
- Repeated 为 nil / 空列表：写一个空条目（`rep`=继承值，`def`=当前值）后结束。
- Repeated 非空：逐个元素处理，元素令 `def` 加 1；第 j≥2 个元素之下的
  首条目的 `rep` 取该 Repeated 节点的层级编号，否则继承外层 rep。

校验按模式深度优先、固定次序进行：每个分组先报未知键中字节序最小者
（`ErrUnknownField`），再按声明次序检查子字段，缺必填报
`ErrMissingRequired`，Go 类型不符报 `ErrType`；全部通过后若任一列条目数
大于 `maxEntries`（恰等通过），报 `ErrTooLarge`。所有错误均可 `errors.Is`
归类，并可用 `ontology.ErrorPath(err)` 取出点号路径。

### 分页规则

每列独立分页，页绝不切开一条记录：

- 当前页条目数已达 `pageEntries`（≥ 即算）时，在追加下一条记录的
  首条目前关闭旧页、另开新页。
- 单条记录条目很多时，页可超过 `pageEntries`。
- `Pages(col)` 返回各页的起始记录序号、记录数与条目数（含尚未关闭的末页）。

### 重装规则

`Assemble` 逐列按记录顺序消费条目：`rep=0` 开启新记录，`rep=k` 表示在
第 k 个 Repeated 节点的列表追加元素；`def` 决定哪些 Optional / Repeated
节点存在。各列构建出的子树按模式深度合并，重复分组的元素个数必须一致，
否则触发携带列路径的 `ErrInconsistent`。重装结果等于原记录的规范化形式：
删除缺失 Optional、nil/空 Repeated，但保留“存在却没有任何叶子”的
Optional 分组为空 `map`。

### 本地验证

```bash
# 全量测试（含 2000 组随机模式/记录与朴素模型对照）
go test ./...

# 竞态检测
go test -race ./...

# 查看随机对照用例打印的输入、输出与判定依据
go test -run TestRandomDifferential -v

go vet ./...
gofmt -l .
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
