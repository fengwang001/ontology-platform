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

## 版本化 BM25 排名器

`bm25` 包提供带版本历史与回收水位的增量 BM25 排名器：

- `Add(docID, terms)`、`Update(docID, terms)`、`Delete(docID)` 成功后将版本号 `V` 加一；初始版本为 `0`，被拒绝的操作不改变文档、统计、版本号或水位。
- 查询使用 `Search(query, k, asOf)`。查询词先去重，只统计文档中实际出现的词项；不含任何查询词的文档不返回。
- 固定参数为 `k1=3/2`、`b=3/4`。版本 `asOf` 中，文档 `d` 对词项 `t` 的贡献为：

```text
idf(t) * tf * 5/2 / (tf + 3/2 * (1/4 + 3/4 * dl/avgdl))
idf(t) = (N - df + 1/2) / (df + 1/2)
avgdl = L / N
```

- `N`、`L`、`df`、`tf`、`dl` 均来自指定版本；所有运算使用 Go `math/big.Rat`，输出固定为既约分数文本 `分子/分母`。
- 排序先按分数降序；分数相同按 `docID` 原始字节序升序（Go 字符串按字节比较），再截取前 `k` 个。
- `Compact(keep)` 将水位 `W` 移到 `keep`，回收 `keep` 之前的历史；`asOf >= W` 才可查询。`keep == W` 成功且为空操作，回收不改变当前状态和 `V`。
- 错误按题面优先级区分：`ErrInvalidArgument`、`ErrDuplicateDocument`、`ErrDocumentNotFound`、`ErrVersionOutOfRange`、`ErrWatermarkRollback`、`ErrVersionCompacted`。
- 增删改与查询通过读写锁给出可串行化顺序，`Update` 的整体替换不会被查询部分观察到；N/L/df 由增量事件维护。
- 当前版本查询后可用 `LastSearchPostingReads()` 读取非导出倒排项计数器；它等于各去重查询词当前倒排表长度之和，不包含无关词表。

### BM25 本地验证

```bash
# 全量测试，包含 2000 组随机操作序列与朴素全量重算对拍
GOCACHE=/tmp/go-cache-ontology PATH=/usr/local/go/bin:$PATH go test ./...

# 并发竞态检测
GOCACHE=/tmp/go-cache-ontology PATH=/usr/local/go/bin:$PATH go test -race ./...

# 查看随机对拍输入、输出和判定依据（测试日志较多）
GOCACHE=/tmp/go-cache-ontology PATH=/usr/local/go/bin:$PATH go test ./bm25 -run TestRandomOperationsAgainstNaiveReplay -v
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
