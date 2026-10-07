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

## 校验钩子机制

对象类型与链接类型的校验钩子注册、分组排序、组间/组内短路、调用快照、
异常上抛与审计记录由 `ontology/validate` 包提供：

- 设计说明（关键取舍、被放弃方案、复杂度论证、本地验证方法）：
  `docs/design-validation-hooks.md`
- 并发正确性由“朴素串行参考模型 + 全序线性化对照”测试覆盖，建议配合 `-race` 运行：

```bash
go test ./ontology/validate -run TestConcurrentLinearizationAgainstNaive -count=30
go test -race ./ontology/validate
```
