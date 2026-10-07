# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `estimation/`：团队估点投票会话服务（隐藏投票、揭示时机、收敛/分歧
  判定、轮次上限与强制取值、成员进出与计时到期下可精确复现）。
  设计说明见 `estimation/DESIGN.md`。

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
