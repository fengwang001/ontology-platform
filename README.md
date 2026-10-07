# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：事件溯源对象状态重建

在对象类型存在继承与属性覆盖（且规则本身可版本化演进）的前提下，
以事件溯源方式重建对象实例在任意历史时刻的状态。详见 [DESIGN.md](DESIGN.md)。

- `ontology/` 核心包：值与取值范围、版本化规则注册表、事件存储、
  检查点加速重建器、朴素重放对照模型、审计日志。
- `cmd/server/` 端到端演示。

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
