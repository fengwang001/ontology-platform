# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology/binding`：链接类型跨对象类型字段绑定依据的兼容性校验
  （对应关系双射/单向判定、缺失取值、字段删除优先结论、版本化重核验、
  链接类型间状态隔离、实例遍历上界、并发线性一致性与审计）。
  设计与取舍见 `docs/design-binding-compatibility.md`。

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
