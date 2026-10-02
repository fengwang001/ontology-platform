# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 信任锚跟踪

RFC 5011 风格信任锚自动更新跟踪器位于 `trustanchor/`，状态转移表、三阶段观测处理、固定拒绝次序和安全不变量见 `trustanchor/README.md`。

```bash
go test ./...
go test -race -v ./trustanchor
go test -v ./trustanchor -run TestRandomSequencesMatchNaiveSimulation
```

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
