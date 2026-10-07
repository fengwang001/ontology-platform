# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多态动作分派

`ontology/` 包实现了“动作通用声明 + 各对象类型注册具体执行逻辑”的多态
分派，覆盖继承向上查找、运行期替换（在途调用不受影响）、放宽声明与事后
审计、查找中并发撤销、四类失败区分、链长上界与并发线性化。设计取舍、被
放弃方案与验证方法见 [`docs/design.md`](docs/design.md)，包级用法见
[`ontology/doc.go`](ontology/doc.go)。

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
