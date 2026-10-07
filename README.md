# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 功能

- **校验钩子机制**（`ontology` 包）：对象类型与链接类型可挂载多个校验钩子，
  支持优先级分组、组内/组间短路、动态注册注销、调用级快照与并发线性一致性。
  设计取舍与验证方法见 [docs/validation-hooks.md](docs/validation-hooks.md)。

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
