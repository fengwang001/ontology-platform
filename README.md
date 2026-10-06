# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `mirror`：多成员镜像块卷服务——成员故障、世代仲裁、脏区记录、
  部分/全量重同步与权威成员选取。设计说明见 `docs/DESIGN.md`。
- `naive`：独立编写的朴素逐块模型，用于与 `mirror` 做随机操作序列对照。

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
