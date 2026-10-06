# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `taxipool`：机场出租车蓄车池虚拟排队与短途返回优先服务。支持按候机楼分区
  排队、放行与跨候机楼调剂、短途返回优先凭证（次数/时效/名额限制）、爽约禁入、
  O(log n) 队列位置查询；全部操作可并发调用且结果可精确复现。设计取舍见
  [DESIGN.md](DESIGN.md)。

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
