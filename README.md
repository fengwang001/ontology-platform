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

## 分代孤儿回收子系统

跨链接类型的分代孤儿回收位于 `orphanreclaim` 包：独立保留 / 联合保留两层判定、
两代宽限期队列、救回清零与到期原子清理。设计取舍、被放弃方案、复杂度证明与
测试—需求覆盖矩阵见 `orphanreclaim/DESIGN.md`；快速开始示例见
`orphanreclaim/example_test.go`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
