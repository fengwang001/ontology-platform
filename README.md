# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology/`：跨对象类型权限传播模块。动作在一次事务中可同时创建、修改
  多个对象类型的实例，并沿链接类型做有界级联传播；权限检查沿传播路径进行，
  支持环去重、深度上限、链接类型排除、不可见实例的整体拒绝/跳过两种互斥
  模式、逐层全过/任一层过两种合并规则，以及全有或全无的原子提交。
  关键取舍与被放弃的方案见 [DESIGN.md](DESIGN.md)。
- `cmd/server/`：最小演示，构造一个小型本体并执行允许/拒绝两组动作，
  打印判定日志与最终实例状态。

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
