# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前已交付 `ontology` 包：对象实例的两种并存并发控制——普通属性更新的
乐观版本号控制，与动作（Action）生命周期状态机转换的独占占用权。

- 设计与取舍说明：[docs/DESIGN.md](docs/DESIGN.md)
- API 使用指南：[docs/USAGE.md](docs/USAGE.md)
- 核心实现：`ontology/store.go`、`ontology/lease.go`、`ontology/decisionlog.go`
- 正确性测试：`ontology/store_test.go`、`ontology/lease_test.go`、
  `ontology/serialmodel_test.go`（随机序列对照独立朴素全局串行模型）、
  `ontology/bench_test.go`（O(1) 判定开销证据）

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 当前交付为库包（无外部依赖，go mod tidy 可选）
go build ./...
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
