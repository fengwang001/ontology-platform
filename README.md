# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 增量导出去重与位点推进

见 `export/DESIGN.md`：位点声明/确认、周期内重试去重、位点介质损坏时基于
历史记录的安全起点推导，以及中断重放等价性。代码位于 `export/`，
随机差分测试与朴素参照模型在 `export/*_test.go`。

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
