# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 包

- `narrowing`：联合类型的流敏感窄化分析器。对由赋值、条件分支与提前返回
  构成的结构化程序，分析每个程序点上各变量窄化后的类型，并对非法使用给出
  可区分的错误（参数非法 / 不可访问属性 / 缺少判别属性 / 不可赋值）。
  设计说明见 `docs/narrowing-design.md`。

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
