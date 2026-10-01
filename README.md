# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 索引序满足判定器（`indexorder` 包）

`indexorder.Catalog` 登记带方向（`ASC`/`DESC`）与空值位置
（`NULLS FIRST`/`NULLS LAST`）的多列索引，并为等值列集合 `eq` 与
`ORDER BY` 列表挑选可免去排序的索引及扫描方向。`Register` / `Drop` /
`Choose` 通过 `sync.RWMutex` 保证并发安全，结果等价于某个串行顺序；
`Choose` 为只读。

### order 规整规则

1. 丢弃列名属于 `eq` 的项（无论方向如何，等值列值固定且非空）。
2. 对剩余项中重复出现的列名只保留第一次出现的项（含其方向与空值位置）。
3. 得到的序列记为 `need`。`need` 为空时任何已登记索引都以正向满足。

### 正反向满足条件

依次考察索引的各列项：列名属于 `eq` 的跳过；否则必须与 `need` 中下一个
未匹配项列名相同，并满足：

- 正向（`FORWARD`）：方向与空值位置都与 need 项相等。
- 反向（`BACKWARD`）：方向与空值位置都与 need 项相反（`ASC`↔`DESC`，
  `NULLS FIRST`↔`NULLS LAST`）。

任一列不符即不满足；`need` 全部匹配后立即满足，后续索引列不再考察；
索引列走完而 `need` 仍有剩余则不满足。

### 选择次序

多个索引均可满足时，按以下次序挑选：

1. 能正向满足者优先于只能反向满足者；
2. 再取索引列项数少者；
3. 再取索引名字节序小者。

结果只依赖当前索引集合与查询，与登记顺序无关；已 `Drop` 的索引不再被选。

### 错误原因（相互可区分，`errors.Is` 判定）

- `Register`（按序只报第一个）：`ErrEmptyName`、`ErrDuplicateName`、
  `ErrEmptyColumns`、`ErrEmptyColumnName`、`ErrInvalidDirection`、
  `ErrInvalidNullsOrder`、`ErrDuplicateColumn`。
- `Drop`：`ErrIndexNotFound`。
- `Choose`：`ErrEqEmptyColumnName`、`ErrOrderEmptyColumnName`、
  `ErrOrderInvalidDirection`、`ErrOrderInvalidNulls`、
  `ErrNoMatchingIndex`。非法输入先于“无索引满足”报告。

被拒绝的操作不改变已登记索引集合。

### 本地验证

```bash
# 全量测试（对拍 2000 组随机索引集合与查询 + 竞态检测，-v 可看输入/输出/判定依据日志）
go test -race -v ./indexorder/

# 全仓库测试与检查
go test ./...
go vet ./...
gofmt -l .
```

测试覆盖：同序正向、全取反反向、只取反方向不满足、eq 列夹在中间被跳过、
order 中 eq 列被丢弃、重复列只保留首次、need 为空、正向优先于更短反向、
列数并列按名字取小，以及与逐列比对朴素实现（`naive_test.go`）对拍
2000 组随机用例、并发访问与操作序列重放确定性。

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
