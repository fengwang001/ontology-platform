# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子包

- `toll/`：高速公路门架计费服务——路径还原、出口结算、迟到记录补扣/退款、
  重复记录识别、车型变更分段计费、月度封顶。设计取舍见 `toll/DESIGN.md`。

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
