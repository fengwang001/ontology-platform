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

## 泛型实例化登记与去重子系统

位于 `genericinst/`，负责泛型定义登记、类型实参规范化、语义等价去重、嵌套
实例化的整体撤销、依赖图与过期传播、配额控制以及并发线性一致性。

- 设计取舍与被放弃方案：`genericinst/DESIGN.md`
- API 与语义说明：`genericinst/doc.go`
- 本地验证：`go test -race -v ./genericinst/`（含与朴素模型的 400 条随机对照）

## 代码检查

```bash
gofmt -l .
go vet ./...
```
