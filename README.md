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

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 履约时效承诺与超时赔付（`fulfillment` 包）

订单被接受时冻结承诺送达时刻，送达后按延误时长分档赔付，
并把延误归因到商家、骑手、平台或用户。设计取舍见
[fulfillment/DESIGN.md](fulfillment/DESIGN.md)。

```bash
go test ./fulfillment/          # 定向覆盖测试
go test -race ./fulfillment/    # 并发正确性
go test -v -run TestRandomizedDifferential ./fulfillment/  # 朴素模型对拍日志
```
