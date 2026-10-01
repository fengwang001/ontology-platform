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

## snippet 包

`snippet/` 实现带文档登记的搜索结果片段选取器：`Register`/`Unregister`
管理 UTF-8 文档（≤ 1 MiB），`Snippets(docID, hits, W, K)` 在字节偏移命中
区间上按固定窗口逐轮贪心选出至多 K 个不重叠片段，并给出严格重叠合并
后的高亮区间与分值。结果确定可重放、并发安全，拒绝原因可区分且被拒绝
操作不改状态。

选取轮次、分值、高亮合并规则与拒绝原因详见 `snippet/README.md`。

本地验证：

```bash
go test ./snippet                                   # 含 2000 组随机对拍
go test -run TestRandomDifferential -v ./snippet   # 打印输入/输出/判定依据
go test -race -v ./snippet
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
