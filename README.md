# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `booking/`：带超订比例、定向交叉可行性校验与候补顺位的合约广告库存预订簿。
  设计、容量/可行性定义、受影响子集推导、候补递补规则与本地验证方法见
  [`docs/booking.md`](docs/booking.md)。

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
