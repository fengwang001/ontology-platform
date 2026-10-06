# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 成绩复核与改分审计引擎

根包 `gradeaudit` 实现成绩版本链、复核申请、教师提案、审批、学期锁定、锁定后特殊通道、历史时点查询和只追加审计账本。

- 设计取舍见 `DESIGN.md`
- API 用法见 `API.md`
- 确定性边界用例与朴素模型随机对照在 `engine_test.go`、`model_test.go`
- 快照查询复杂度验证微基准在 `benchmark_test.go`

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

当前环境若 Go 未在 PATH 中，可使用：

```bash
GOCACHE=/tmp/go-build PATH=/usr/local/go/bin:$PATH go test ./...
GOCACHE=/tmp/go-build PATH=/usr/local/go/bin:$PATH go test -race ./...
GOCACHE=/tmp/go-build PATH=/usr/local/go/bin:$PATH go vet ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
