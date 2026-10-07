# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 结构

- `ontology/` —— 核心库：对象/链接存储、动作执行引擎（前置/后置校验职责分离）、
  声明矛盾静态分析、乐观并发控制、审计日志与失败轨迹、朴素串行参照实现。
- `cmd/server/` —— 演示程序：装配引擎并演示四类执行结果。
- `docs/design.md` —— 设计说明：关键取舍、被放弃的方案与本地验证方法。

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
