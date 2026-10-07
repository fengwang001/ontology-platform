# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology/`：对象实例存储，支持属性级并发写入判定（写集合 ∪ 相关读集合
  交集判定）、严格单调版本号、不可变快照、跨实例钩子读取集合传播与
  可重放的判定日志。
- `ontology/naive/`：朴素全锁定对照模型，用于随机并发等价性测试。
- `cmd/server/`：最小演示。
- `docs/design.md`：设计说明（关键取舍、被放弃的方案、本地验证方法）。

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
