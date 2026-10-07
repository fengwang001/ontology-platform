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

## 派生属性索引一致性子系统

跨链接派生索引（下游实例的键值取自其经链接连接到的源实例属性，支持多级传递），
保证源属性写入、链接增删、实例删除及三者并发交织下的原子一致性与串行等价性。

- 设计说明（取舍、被放弃方案、形式化不变量）：`docs/design.md`
- 实现：包 `ontology`（schema/事务工作副本/分层传播/查询/错误优先级/日志）
- 独立朴素重算模型与随机差分对拍：包 `ontologytest`
- 可运行演示：`go run ./cmd/demo`

```bash
go test -race ./...
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out
```
