# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 查询展开器（`ontology` 包）

`expander.go` 实现了一个带近期点选加成的边输入边搜索（search-as-you-type）
查询展开器，所有操作并发安全（内部单一互斥锁，结果等价于某一串行顺序）。

### 数据操作

- `Index(docID, terms)`：登记文档。词项为不含 ASCII 空格 `0x20` 的非空字符串，
  区分大小写、按字节序比较；同一词项在文档内重复只算一次。
- `Remove(docID)`：删除文档；删除后再以同一 `docID` 登记视为全新文档。
- `Pick(term)`：记录一次点选；词项必须当前存在于某文档中，但点选记录本身
  与文档增删无关（词项消失后记录保留，重新出现并成为候选时照常计分）。

### 查询串解析（`Expand(query, maxExp)`）

- 只有 ASCII 空格 `0x20` 是分隔符，连续空格视为一个。
- 查询串以空格结尾：所有词都是完整词；不以空格结尾：最后一个词是前缀词，
  其余是完整词。没有前缀词时没有候选。
- 空串或只含空格报 `ErrEmptyQuery`；词数超过 8（含重复词）报
  `ErrQueryTooLong`；`maxExp` 不在 1..1000 报 `ErrInvalidMaxExp`。
  查询中的词不在词典里不是错误，只产生空结果。

### 条件文档集与条件 df

- 条件文档集 = 包含全部（去重后的）完整词的文档；没有完整词时为全部文档；
  任一完整词不存在则条件文档集为空。
- 候选词项 = 出现在条件文档集内、以前缀词为前缀（含等于前缀词本身）的词项。
- 条件 df = 条件文档集中包含该词项的文档数（不是全局 df）；条件 df 为 0 的
  词项不是候选，不占名额，也不计入截断判定。

### 点选窗口与得分

- 合并器维护计数 `T`，即成功的 `Expand` 次数（初值 0；结果为空的成功
  Expand 也加 1，被拒绝的 Expand 不加 1）。
- 每次成功 Expand 先令 `T += 1` 得到本次 `t`；`Pick(term)` 记录 `(term, c)`，
  `c` 为调用时刻的 `T`；同一 `c` 下同一词项重复 Pick 只保留一条。
- 有效点选数 = 满足 `t - c <= 3` 的记录条数（Pick 之后第 1、2、3 次 Expand
  有效，第 4 次起 `t-c=4` 失效；失效记录不计数也不删除）。
- 候选得分 = `条件 df + 2 × 有效点选数`。

### 排序、截断与命中文档

- 候选按（得分降序、词项字节序升序）排序，取前 `maxExp` 个；
  `Truncated` 当且仅当候选总数大于 `maxExp`。
- 命中文档 = 条件文档集中至少包含一个**被保留候选**的文档，按 docID
  字节序升序返回。没有前缀词时命中文档就是条件文档集。
- 返回切片均为独立拷贝，不别名内部状态；相同操作序列重放结果完全一致。

### 拒绝原因（按顺序只报第一个，拒绝不改任何状态）

- `Index`：`ErrInvalidArgument`（docID 为空 / terms 为空或含空串或含空格），
  其次 `ErrDuplicateDoc`。
- `Remove`：`ErrDocNotFound`。
- `Pick`：`ErrInvalidArgument`，其次 `ErrTermNotFound`。
- `Expand`：`ErrEmptyQuery`、`ErrQueryTooLong`、`ErrInvalidMaxExp`。

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

查询展开器的测试包含：

- 覆盖末词完整/前缀、前缀恰等于词项、截断边界、条件 df vs 全局 df、
  并列字节序、点选窗口边界（t-c=3 有效 / =4 失效）、同一 c 去重与不同 c
  累计、被拒绝 Expand 不推进 T、空结果 Expand 推进 T、词项删除后重现、
  拒绝原因顺序与状态不变性、返回切片不别名等定向用例（`expander_test.go`）。
- 与逐文档扫描的朴素实现对拍 2000 组随机操作序列（`fuzz_test.go`），
  日志打印每组的输入、两侧输出与判定依据：

  ```bash
  go test -run TestFuzzAgainstNaive -v
  ```

- 并发安全（配合竞态检测）：

  ```bash
  go test -race -run TestConcurrentExercises -v
  ```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
