# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 押金争议处理服务

租赁押金退还与扣减争议处理服务位于 `deposit/` 包：

- 设计与关键取舍：`deposit/DESIGN.md`（含被放弃方案与本地验证方法）
- API 说明：`deposit/doc.go`
- 边界测试：`deposit/service_test.go`
- 朴素模型随机差分测试（逐步输入/输出/判定依据日志）：`deposit/diff_test.go`、`deposit/naive_model_test.go`

```bash
go test -race -count=1 ./deposit/
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
