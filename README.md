# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 模块

- `audit`：权限决策审计回放模块——版本化权限规则、追加式审计记录、
  历史回放、纠正记录链与三态合法性查询。设计说明见 `docs/design.md`。

## 运行演示

```bash
go run ./cmd/auditdemo
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
