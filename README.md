# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前已交付：**链接基数约束 × 属性级权限联合仲裁子系统**（根包 `ontology`），
由基数账本（`ledger.go`）、权限继承与覆盖解析（`permissions.go`）、
联合仲裁与错误归一化（`arbiter.go`）三模块组成，另含一个全量重遍历的
朴素对照模型（`naive.go`）用于随机差分测试。设计取舍与需求对应见
[DESIGN.md](DESIGN.md)。

```bash
go run ./cmd/server   # 仲裁链路演示
go test -race ./...
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
