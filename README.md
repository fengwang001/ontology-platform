# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 读取时裁决模块

`ontology/` 包实现行级可见性谓词 + 属性级读/写/遮蔽的统一裁决，并对写入
路径做对称强制检查。设计取舍见 [DESIGN.md](DESIGN.md)，接入方式见
[docs/USAGE.md](docs/USAGE.md)，可运行示例见
`ontology/example_test.go`。

关键测试：

- 朴素参照随机差分：`go test ./ontology/ -run TestDifferential -v`
- 并发线性化（竞态）：`go test -race ./ontology/ -run Concurrent`
- 开销与策略/实例规模无关的可观测证明：
  `go test ./ontology/ -run TestTouchedPolicies -v`

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
