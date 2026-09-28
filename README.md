# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前仓库提供核心基础组件 `ontology` 包：**在表结构持续演进下，按列的稳定标识把任意历史版本的事件解码为当前最新结构的一行**，保证列的新增、删除、改名都不会让历史数据错位。

## 环境要求

- Go 1.26+（`go version` 确认；如 `go` 不在 PATH，使用 `/usr/local/go/bin`）

## 组件说明：版本化表结构与历史事件解码

包路径：`ontology/ontology`，入口类型为 `ontology.Registry`。

### 1. 列的稳定标识（ColumnID）

- 每列有三要素：稳定标识 `ID`、当前名字 `Name`、当前默认值 `Default`。
- **标识一经分配永不复用**：新增列分配单调递增的新 ID；删除列不会释放其 ID；之后再新增同名列，拿到的是全新 ID，与被删除列毫无关系。
- 标识是解码时唯一的取值依据，名字与位置都不参与取值。

### 2. 演进操作

初始结构为版本 1，此后每次成功的演进追加一个**不可变版本快照**（版本号 +1）：

| 操作 | 语义 |
| --- | --- |
| `AddColumn(name, default)` | 只能在**末尾**加列，分配新 ID；名字非空且不与现存列重名（已删除列名可复用）。 |
| `DropColumn(id)` | 按标识删除该列；至少保留一列；被删 ID 永不复用。 |
| `RenameColumn(id, newName)` | **只改名字**，ID、位置、默认值不变；新名字非空且不与他列冲突；改成同名视为无操作，不发布新版本。 |

非法演进（空名、重名、操作不存在的 ID、删除唯一列等）一律拒绝。

### 3. 解码规则

`Decode(version, values)` / `DecodeBatch(events)`：

1. 事件值首先按**其写入版本的列位置**对齐到该版本的列 ID；
2. 再按 ID 投影到当前最新结构——整个过程只认 ID，不看名字、不看当前位置；
3. 当前结构里有、但事件版本里没有的列（在该事件之后才新增），取**当前结构默认值**，结果单元标记 `FromDefault=true`；
4. 事件版本里有、但当前结构已删除的列，其值被丢弃，不出现在结果中；
5. 改名列照常携带原事件值，只是结果中的名字变为新名字；
6. **空串是合法载荷值**：事件中显式给出的 `""` 走事件分支（`FromDefault=false`），与“该列在事件版本中不存在”严格区分，哪怕该列默认值也恰好是空串。

结果 `Row` 完整基于某一个当前版本快照；相同输入反复解码得到完全相同的输出。

### 4. 拒绝原因（可区分、可程序化处理）

所有拒绝返回 `*ontology.Error`，可用 `errors.Is` 匹配哨兵：

| Kind / 哨兵 | 含义 |
| --- | --- |
| `ErrInvalidSchema` | 初始结构非法（无列、空列名、重名、版本上限配置 < 1）。 |
| `ErrVersionNotFound` | 事件声称的版本不存在。 |
| `ErrValueCountMismatch` | 事件值个数与所在版本列数不符。 |
| `ErrInvalidEvolution` | 演进操作非法。 |
| `ErrVersionLimitExceeded` | 版本数超过 `WithMaxVersions` 上限（默认 10000）。 |

关键保证：**被拒绝的操作不改变任何状态**——版本列表不变、ID 不被消耗（失败的新增不会让下一个 ID 跳号）。批量解码中任一事件被拒，整批失败且不返回任何部分结果。

### 5. 并发模型

- 演进在写锁下只做“校验 + 追加不可变快照”；
- 解码在读锁下钉住“当前最新快照”和“事件版本快照”两个不可变对象，行的实际构造在锁外完成；
- 因此解码可并发、也可与演进并发，且每个结果都完整自洽于某一版本，不会混用两个版本的结构。

### 6. 日志

默认使用 `slog.Default()`，可用 `WithLogger` 注入。每次解码都会记录：输入版本与输入值、解码结果单元、结果所基于的版本，以及判定依据（按输入版本位置对齐到列 ID、缺失列取当前默认值）；拒绝记录原因类别与细节。

### 用法示例

```go
r, _ := ontology.NewRegistry([]ontology.ColumnSpec{
    {Name: "user", Default: "unknown"},
    {Name: "city", Default: "-"},
})

r.AddColumn("email", "")              // v2：末尾加列，新 ID
r.RenameColumn(1, "account")          // v3：只改名
r.DropColumn(2)                       // v4：删除 city
r.AddColumn("city", "unknown-city")   // 同名列复活，但拿到全新 ID

// v1 事件：值按 v1 位置 [user, city] 对齐到 ID
row, err := r.Decode(1, []string{"alice", ""})
// account(id=1) = "alice"（事件值，跟随改名）
// email(id=3)   = ""（v1 无此列，取当前默认值，FromDefault=true）
// city(id=4)    = "unknown-city"（新 ID，与被删的旧 city id=2 无关，FromDefault=true）
// 注：事件里旧 city(id=2) 的显式空串随该列删除而丢弃
```

## 本地验证

```bash
# 编译与静态检查
go build ./...
go vet ./...
gofmt -l .

# 全量测试
go test ./...

# 带竞态检测（验证并发解码与演进）
go test -race ./...

# 详细输出
go test -race -v ./ontology/

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

测试覆盖：删除后新增同名列得到新标识且旧值不泄漏、空串与缺列的区分、按位置/按名字映射的错误对照、各类非法输入、非法演进不改变版本列表与 ID 分配、版本超限拒绝、批量解码原子性、重复解码确定性，以及解码与演进高并发下的竞态安全。
