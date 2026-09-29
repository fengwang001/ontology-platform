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

## 窗口状态管理器（清理 / 复活语义）

实现位于 `ontology/`（包 `ontology`），提供带阈值触发、清理冻结、迟到事件复活与
回收互斥语义的并发安全窗口状态管理器。状态迁移、复活与回收互斥规则及测试说明见
[`ontology/README.md`](ontology/README.md)。

```bash
# 竞态检测 + 重复运行 + 详细判定日志
go test -race -count=3 -v ./ontology/
```
