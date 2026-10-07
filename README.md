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

## 权限传播模块

跨对象类型、沿链接类型传播的事务内权限检查与执行模块位于 `ontology/`，
独立朴素参照位于 `ontology/naive/`，大规模随机差分测试位于
`ontology/difftest/`。

- 设计说明（关键取舍、被放弃方案、本地验证）：[`docs/DESIGN.md`](docs/DESIGN.md)
- 使用指南：[`docs/USAGE.md`](docs/USAGE.md)

关键能力：有限深度 + 环去重传播、`NoPropagate`、不可见实例的
拒绝/跳过两种互斥模式、`MergeAll/MergeAny` 顺序无关合并、
全有或全无提交、固定错误优先级、可串行化并发，以及只随实际触及
实例数增长的检查次数（由 `Report.Checks` 可观测证明）。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
