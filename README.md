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
GOCACHE=/tmp/go-cache go test ./...

# 带竞态检测与详细输出
GOCACHE=/tmp/go-cache go test -race -v ./...

# 单个包 / 单个用例
GOCACHE=/tmp/go-cache go test ./specimen
GOCACHE=/tmp/go-cache go test -run TestRandomSequencesAgainstNaiveModel ./specimen

# 覆盖率
GOCACHE=/tmp/go-cache go test -coverprofile=coverage.out ./...
GOCACHE=/tmp/go-cache go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 临床检验标本模块

核心实现位于 `specimen`，设计说明见 `specimen/DESIGN.md`。包内包含目录登记、申请、采集、送出、签收、取消和查询 API；随机朴素模型对照、竞态测试和两档复杂度基准均在 `specimen/*_test.go`。
