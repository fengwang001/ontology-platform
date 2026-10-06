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

## 可增长协程栈子系统（`stackmgr/`）

- 设计说明：`stackmgr/DESIGN.md`（关键取舍、被放弃方案、本地验证）。
- 测试：`go test ./stackmgr/ -v -run TestDifferential` 查看与朴素模型
  逐条对照的输入/实际输出/判定依据日志；`go test -race ./...` 验证并发配额。
