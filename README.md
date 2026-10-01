# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。当前 Go 包提供带句界、总间隙和单步间隙约束的并发短语邻近匹配器。

## 短语邻近匹配

`ontology.NewMatcher()` 返回线程安全的匹配器：

- `Add(docID, terms)` 登记文档，`terms` 的下标即位置（从 0 开始）；词项可重复，但不能为空串。
- `Delete(docID)` 删除文档；删除后再用相同 `docID` 登记视为新文档。
- `Phrase(query, slop, maxGap, k)` 查询 1 到 8 个词项组成的短语，按 `freq` 降序、`minGap` 升序、`docID` 字节序升序返回前 `k` 项。

特殊词项 `"<S>"` 是句界，占一个位置。查询词不能使用该词项；一次匹配的闭区间 `[p1, pn]` 内不能出现句界，因此匹配不能跨句，也不能把句界当作普通词。

对于长度为 `n` 的查询，匹配位置必须严格递增：

```text
p1 < p2 < ... < pn
```

第 `i` 个位置必须等于查询的第 `i` 个词，查询词不能颠倒或复用同一位置。每个单步间隙必须满足：

```text
p[i+1] - p[i] - 1 <= maxGap
```

总间隙必须满足：

```text
sum(p[i+1] - p[i] - 1) = pn - p1 - (n - 1) <= slop
```

因此总间隙只由首尾位置决定，但每个中间词仍需同时满足单步上限，不能简单地总取最近出现位置。

每篇文档的 `freq` 是存在合法匹配的不同首位置 `p1` 的数量；同一首位置即使有多条中间路径也只计一次，首位置不同且区间重叠则分别计数。`minGap` 是该文档所有合法匹配中的最小总间隙。单词查询的 `freq` 等于该词在全文中的普通位置数，`minGap` 为 0。

错误按指定顺序只返回第一个原因，且被拒绝的操作不修改索引：

- `ErrInvalidDocument`：`docID == ""`、`terms` 为空或含空串。
- `ErrDuplicateDocID`：新增时 `docID` 已存在。
- `ErrDocNotFound`：删除不存在的文档。
- `ErrInvalidQuery`：查询为空、超过 8 项、含空串或含 `"<S>"`。
- `ErrInvalidGap`：`slop` 或 `maxGap` 不在 0 到 100。
- `ErrInvalidK`：`k < 1`。

没有文档包含全部查询词项时返回空结果和空错误。读写通过读写锁保证并发调用等价于某个串行顺序。包内非导出计数器记录一次 `Phrase` 实际读取的倒排位置项数；只对去重后的查询词项计数，重复查询词不重复累加，所以该计数不超过各去重查询词倒排位置总长之和，也不会随只含无关词的文档数增长。

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

本项目测试包含严格相邻、总间隙等于 `slop`、单步间隙等于 `maxGap`、更远中间词合法、句界边界、重复词位置不复用、重叠首位置、排序、删除重建、并发确定性、1000/100000 篇无关文档的读档对比，以及 2000 组与逐首位置朴素回溯实现的随机对拍。对拍日志用 `go test -v` 查看，包含输入、输出和判定依据。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
