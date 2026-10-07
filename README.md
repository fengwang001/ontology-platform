# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology/` — 基于角色与标签的访问控制模块：
  标签挂在对象类型上并沿链接类型传播（支持多父、多路径、含环、
  阻断点），实例按归属继承标签；角色授权支持允许/显式拒绝与上级
  继承，按 deny-overrides 裁决；判定结果附完整裁决依据与结构化日志。
  设计取舍与验证方法见 [docs/design.md](docs/design.md)。
- `cmd/server/` — 演示程序。

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
