# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块：分层备份保留管理

`backupretention` 包实现带依赖链保护的分层备份保留服务：全量/增量登记、
日周月三层代表选举、损坏标记的可恢复性传播、法律保留与过期备份批量清理，
任意时刻可精确复现“保留谁、删除谁、为什么”。

- 设计说明（取舍、放弃方案、复杂度证明、本地验证）：`backupretention/DESIGN.md`
- 包级 API 文档：`backupretention/doc.go`，可运行示例：`Example()`
- 朴素参照模型与随机差分测试（打印每条操作输入/输出/判定依据）：
  `backupretention/diff_test.go`、`backupretention/naivemodel_test.go`

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache
go test -race ./backupretention
go test -v ./backupretention -run TestNaiveDifferential
go test ./backupretention -bench Benchmark -run '^$'
```

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
