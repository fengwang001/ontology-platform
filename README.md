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

## 边输入边搜索查询展开器

实现位于 `ontology/expander.go`：

- `Index(docID, terms)` 登记文档并在文档内对词项去重；`Remove(docID)` 删除后可用同一 `docID` 重新登记为新文档。
- 词项和 `docID` 区分大小写，按 Go 字符串字节序排序；词项不能为空，也不能包含 ASCII 空格 `0x20`。
- 查询只以 ASCII 空格分隔，连续空格等价于一个分隔符。查询以空格结尾时所有词都是完整词；否则最后一个词是末词前缀。
- 条件文档集包含全部去重后的完整词；没有完整词时是当前全部文档；任一完整词不存在时为空集。查询词不在词典中不是错误。
- 候选词项必须出现在条件文档集内且以前缀词为前缀；前缀词本身也可以是候选。条件 df 只统计条件文档集内包含该词项的文档数，条件 df 为 0 的词项不是候选。
- `Pick(term)` 记录 `(term, T)`，其中 `T` 是成功 `Expand` 次数；同一 `T` 下重复点选同一词项只保留一条。删除文档不会删除点选记录。
- 每次参数合法的 `Expand` 先把 `T` 加一；当 `t-c <= 3` 时点选记录有效，得分是 `条件 df + 2 * 有效点选数`。
- 候选按得分降序、词项字节序升序排序，取前 `maxExp` 个；候选总数大于 `maxExp` 时 `Truncated=true`。
- 没有前缀词时不产生候选且 `Truncated=false`，命中文档就是条件文档集；有前缀词时，命中文档是条件文档集中包含任一保留候选的文档，按 `docID` 字节序返回。
- 被拒绝的操作不修改索引、点选记录或 `T`；结果为空的合法 `Expand` 仍会递增 `T`。所有方法用互斥保护，返回结果是内部状态的副本。

### 本地验证

如果 `go` 不在 `PATH`，本环境可使用 `/usr/local/go/bin/go`，并指定可写缓存：

```bash
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/gofmt -w ontology/*.go
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test ./...
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go test -race -v ./ontology
GOCACHE=/tmp/go-cache-ontology /usr/local/go/bin/go vet ./...
```

`TestRandomDifferentialAgainstNaiveScanner` 重放 2000 组随机操作序列，并在测试日志中输出每一步输入、实际输出、朴素扫描模型输出和判定依据。
